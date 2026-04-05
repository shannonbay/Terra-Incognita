package harness

import (
	"fmt"
	"strings"
	"sync"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ConversationManager maintains per-agent Claude API message histories across ticks.
type ConversationManager struct {
	mu              sync.RWMutex
	histories       map[string]*agentConv
	maxHistoryTicks int
}

// agentConv is the per-agent conversation state.
type agentConv struct {
	// messages is the Claude API message history (user/assistant alternating).
	// Each tick contributes exactly 3 messages:
	//   [user: perception] [assistant: reasoning+tool_use] [user: tool_results]
	messages []anthropic.MessageParam

	// notes accumulates text from record_finding tool calls, prepended each tick.
	notes string

	// compacted accumulates summaries of ticks that were trimmed from history.
	compacted string

	// tickCount tracks how many full ticks are stored in messages.
	tickCount int
}

// NewConversationManager creates a manager with the given per-agent history limit.
func NewConversationManager(maxHistoryTicks int) *ConversationManager {
	if maxHistoryTicks <= 0 {
		maxHistoryTicks = 20
	}
	return &ConversationManager{
		histories:       make(map[string]*agentConv),
		maxHistoryTicks: maxHistoryTicks,
	}
}

// Get returns the conversation state for an agent, creating it if necessary.
func (cm *ConversationManager) Get(agentID string) *agentConv {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	c, ok := cm.histories[agentID]
	if !ok {
		c = &agentConv{}
		cm.histories[agentID] = c
	}
	return c
}

// AppendUser adds the perception user message for the current tick.
// It prepends notes and compacted context if present.
func (cm *ConversationManager) AppendUser(agentID string, tick int, perception map[string]any, availableActions []map[string]any) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	c := cm.getOrCreate(agentID)

	text := buildUserMessage(tick, perception, availableActions, c.notes, c.compacted)
	c.messages = append(c.messages, anthropic.NewUserMessage(anthropic.NewTextBlock(text)))
}

// AppendAssistantTurn adds the assistant response and tool results to history.
func (cm *ConversationManager) AppendAssistantTurn(agentID string, assistantMsg anthropic.MessageParam, toolResults []anthropic.ContentBlockParamUnion) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	c := cm.getOrCreate(agentID)

	c.messages = append(c.messages, assistantMsg)
	if len(toolResults) > 0 {
		c.messages = append(c.messages, anthropic.NewUserMessage(toolResults...))
	}
	c.tickCount++
	cm.trimIfNeeded(c)
}

// AppendFinding appends text from a record_finding call to the agent's persistent notes.
func (cm *ConversationManager) AppendFinding(agentID, text string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	c := cm.getOrCreate(agentID)
	if c.notes == "" {
		c.notes = text
	} else {
		c.notes = c.notes + "\n" + text
	}
}

// Notes returns the current accumulated notes for an agent.
func (cm *ConversationManager) Notes(agentID string) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if c, ok := cm.histories[agentID]; ok {
		return c.notes
	}
	return ""
}

// Messages returns a copy of the current message history for an agent.
func (cm *ConversationManager) Messages(agentID string) []anthropic.MessageParam {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	c, ok := cm.histories[agentID]
	if !ok {
		return nil
	}
	out := make([]anthropic.MessageParam, len(c.messages))
	copy(out, c.messages)
	return out
}

// Reset clears conversation state for an agent (e.g. on new run).
func (cm *ConversationManager) Reset(agentID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.histories, agentID)
}

// ResetAll clears all conversation state.
func (cm *ConversationManager) ResetAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.histories = make(map[string]*agentConv)
}

// --- internal ---

func (cm *ConversationManager) getOrCreate(agentID string) *agentConv {
	c, ok := cm.histories[agentID]
	if !ok {
		c = &agentConv{}
		cm.histories[agentID] = c
	}
	return c
}

// trimIfNeeded drops the oldest tick's 3 messages when over the limit.
// The dropped user message text is appended to compacted for context continuity.
func (cm *ConversationManager) trimIfNeeded(c *agentConv) {
	for c.tickCount > cm.maxHistoryTicks && len(c.messages) >= 3 {
		// Extract the dropped turn's user message text for compaction.
		dropped := c.messages[0]
		if len(dropped.Content) > 0 {
			if blk := dropped.Content[0].OfText; blk != nil {
				summary := compactSummary(blk.Text)
				if c.compacted == "" {
					c.compacted = summary
				} else {
					c.compacted = c.compacted + "\n" + summary
				}
			}
		}
		// Drop the 3 messages for this tick.
		c.messages = c.messages[3:]
		c.tickCount--
	}
}

// compactSummary extracts a brief summary from a full tick user message for the compacted context.
func compactSummary(fullText string) string {
	// Extract tick number if present ("Tick N.")
	if idx := strings.Index(fullText, "Tick "); idx >= 0 {
		end := strings.Index(fullText[idx:], "\n")
		if end < 0 {
			end = len(fullText) - idx
		}
		return "[trimmed: " + strings.TrimSpace(fullText[idx:idx+end]) + "]"
	}
	return "[trimmed tick]"
}

// buildUserMessage constructs the user message text for a given tick.
func buildUserMessage(tick int, perception map[string]any, availableActions []map[string]any, notes, compacted string) string {
	var b strings.Builder

	if notes != "" {
		b.WriteString("Your current notes:\n")
		b.WriteString(notes)
		b.WriteString("\n\n")
	}
	if compacted != "" {
		b.WriteString("Earlier context (trimmed):\n")
		b.WriteString(compacted)
		b.WriteString("\n\n")
	}

	b.WriteString(fmt.Sprintf("Tick %d. Your current perception:\n", tick))

	// Render perception as key: value lines for readability.
	for k, v := range perception {
		b.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
	}

	if len(availableActions) > 0 {
		b.WriteString("\nAvailable actions:\n")
		for _, a := range availableActions {
			name, _ := a["name"].(string)
			desc, _ := a["description"].(string)
			if desc != "" {
				b.WriteString(fmt.Sprintf("  - %s: %s\n", name, desc))
			} else {
				b.WriteString(fmt.Sprintf("  - %s\n", name))
			}
		}
	}

	return b.String()
}
