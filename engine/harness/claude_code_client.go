package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// claudeCodeProvider calls the `claude` CLI as a subprocess, using whatever
// auth is active (Claude.ai Pro/Max plan login or API key). Sessions handle
// conversation history automatically; notes are tracked separately.
type claudeCodeProvider struct {
	config   HarnessConfig
	sessions sync.Map // agentID → sessionID string
	notes    sync.Map // agentID → accumulated notes string
}

func newClaudeCodeProvider(cfg HarnessConfig) *claudeCodeProvider {
	return &claudeCodeProvider{config: cfg}
}

// ccResponse is what we ask Claude Code to return as JSON.
type ccResponse struct {
	Reasoning     string   `json:"reasoning"`
	RecordFinding string   `json:"record_finding,omitempty"`
	Actions       []Action `json:"actions"`
}

// Decide spawns `claude -p` and parses the structured response.
func (p *claudeCodeProvider) Decide(ctx context.Context, agentID string, req DecideRequest) (*DecideResult, error) {
	start := time.Now()

	// Build the user message (perception + available actions + notes).
	notes, _ := p.notes.Load(agentID)
	notesStr, _ := notes.(string)
	userMsg := buildUserMessage(req.Tick, req.Perception, req.AvailableActions, notesStr, "")

	// Build CLI arguments.
	args := p.buildArgs(agentID, req.SystemPrompt, userMsg)

	cmd := exec.CommandContext(ctx, "claude", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude subprocess: %w (stderr: %s)", err, stderr.String())
	}

	// Parse NDJSON output.
	sessionID, resultText := parseClaudeCodeOutput(stdout.Bytes())

	// Persist session ID for subsequent ticks.
	if sessionID != "" {
		p.sessions.Store(agentID, sessionID)
	}

	// Extract structured response from the result text.
	cc, reasoningText := extractCCResponse(resultText)

	latency := time.Since(start).Milliseconds()

	// Persist notes.
	if cc.RecordFinding != "" {
		existing, _ := p.notes.Load(agentID)
		existingStr, _ := existing.(string)
		if existingStr == "" {
			p.notes.Store(agentID, cc.RecordFinding)
		} else {
			p.notes.Store(agentID, existingStr+"\n"+cc.RecordFinding)
		}
	}

	actions := cc.Actions
	if actions == nil {
		actions = doNothingFallback(req.AvailableActions)
	}

	return &DecideResult{
		Actions:       actions,
		ReasoningText: reasoningText,
		FindingText:   cc.RecordFinding,
		LatencyMs:     latency,
	}, nil
}

func (p *claudeCodeProvider) Notes(agentID string) string {
	v, _ := p.notes.Load(agentID)
	s, _ := v.(string)
	return s
}

func (p *claudeCodeProvider) Reset(agentID string) {
	p.sessions.Delete(agentID)
	p.notes.Delete(agentID)
}

// buildArgs constructs the `claude` CLI argument list.
func (p *claudeCodeProvider) buildArgs(agentID, systemPrompt, userMsg string) []string {
	args := []string{
		"-p", userMsg,
		"--output-format", "json",
		"--bare", // skip CLAUDE.md, hooks, MCP servers
	}

	if v, ok := p.sessions.Load(agentID); ok {
		// Resume existing session — history is preserved automatically.
		args = append(args, "--resume", v.(string))
	} else {
		// First tick: set the system prompt.
		// Append JSON response format instructions so the model knows what to return.
		fullSystemPrompt := systemPrompt + ccFormatInstructions()
		args = append(args, "--system-prompt", fullSystemPrompt)
	}

	if p.config.Model != "" {
		args = append(args, "--model", p.config.Model)
	}

	return args
}

// ccFormatInstructions returns the JSON format instructions appended to the system prompt.
func ccFormatInstructions() string {
	return `

---
RESPONSE FORMAT: Reply with ONLY a valid JSON object and nothing else:
{
  "reasoning": "your step-by-step thinking",
  "record_finding": "optional note to keep in memory across ticks (omit key if not needed)",
  "actions": [{"name": "action_name", "params": {"key": "value"}}]
}
The "actions" array is required every tick. Use do_nothing if you choose not to act.`
}

// parseClaudeCodeOutput reads the NDJSON lines from `claude --output-format json`.
// It returns the session ID (from the system init event) and the result text.
func parseClaudeCodeOutput(data []byte) (sessionID, result string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg struct {
			Type      string          `json:"type"`
			Subtype   string          `json:"subtype"`
			SessionID string          `json:"session_id"`
			Result    string          `json:"result"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "system":
			if msg.SessionID != "" {
				sessionID = msg.SessionID
			}
			// session_id may also be nested in data
			if sessionID == "" && len(msg.Data) > 0 {
				var d struct {
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal(msg.Data, &d) == nil && d.SessionID != "" {
					sessionID = d.SessionID
				}
			}

		case "result":
			if msg.SessionID != "" {
				sessionID = msg.SessionID
			}
			result = msg.Result
		}
	}
	return sessionID, result
}

// extractCCResponse parses a ccResponse JSON object from the result text.
// The model is instructed to return only JSON, but we handle extra text gracefully.
// Returns the parsed response and any non-JSON prefix as reasoning text.
func extractCCResponse(text string) (ccResponse, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ccResponse{Actions: []Action{}}, ""
	}

	// Find the outermost JSON object.
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		// No JSON found; treat entire text as reasoning, return do_nothing.
		return ccResponse{Actions: []Action{}}, text
	}

	reasoning := strings.TrimSpace(text[:start])
	jsonStr := text[start : end+1]

	var cc ccResponse
	if err := json.Unmarshal([]byte(jsonStr), &cc); err != nil {
		// Malformed JSON; treat full text as reasoning.
		return ccResponse{Actions: []Action{}}, text
	}

	// If reasoning is embedded in the JSON, prefer that.
	if reasoning == "" {
		reasoning = cc.Reasoning
	} else if cc.Reasoning != "" {
		reasoning = reasoning + "\n" + cc.Reasoning
	}

	return cc, reasoning
}

// Ensure claudeCodeProvider satisfies the interface.
var _ Provider = (*claudeCodeProvider)(nil)
