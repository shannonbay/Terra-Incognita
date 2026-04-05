package harness

import (
	"context"
)

// Provider is the interface both the Anthropic API client and the Claude Code
// subprocess client implement. Server holds one Provider.
type Provider interface {
	// Decide makes one agent decision for the given tick.
	Decide(ctx context.Context, agentID string, req DecideRequest) (*DecideResult, error)
	// Notes returns accumulated record_finding notes for an agent.
	Notes(agentID string) string
	// Reset clears all state for an agent (call between runs).
	Reset(agentID string)
}

// --- API provider (wraps LLMClient + ConversationManager) ---

type apiProvider struct {
	llm  *LLMClient
	conv *ConversationManager
}

func newAPIProvider(cfg HarnessConfig, llm *LLMClient) *apiProvider {
	return &apiProvider{
		llm:  llm,
		conv: NewConversationManager(cfg.MaxHistoryTicks),
	}
}

func (p *apiProvider) Decide(ctx context.Context, agentID string, req DecideRequest) (*DecideResult, error) {
	p.conv.AppendUser(agentID, req.Tick, req.Perception, req.AvailableActions)
	messages := p.conv.Messages(agentID)

	result, err := p.llm.Decide(ctx, req.SystemPrompt, messages)
	if err != nil {
		return nil, err
	}

	if result.FindingText != "" {
		p.conv.AppendFinding(agentID, result.FindingText)
	}
	if result.RawResponse != nil {
		assistantMsg := result.RawResponse.ToParam()
		toolResults := BuildToolResults(result.RawResponse)
		p.conv.AppendAssistantTurn(agentID, assistantMsg, toolResults)
	}
	return result, nil
}

func (p *apiProvider) Notes(agentID string) string {
	return p.conv.Notes(agentID)
}

func (p *apiProvider) Reset(agentID string) {
	p.conv.Reset(agentID)
}

// Ensure apiProvider satisfies the interface at compile time.
var _ Provider = (*apiProvider)(nil)
