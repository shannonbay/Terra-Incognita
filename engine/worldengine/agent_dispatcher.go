package worldengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ---------------------------------------------------------------------------
// Wire types — spec §9.2
// ---------------------------------------------------------------------------

// decideRequest is the JSON body sent to POST /decide.
type decideRequest struct {
	AgentID          string           `json:"agent_id"`
	Tick             int              `json:"tick"`
	Perception       map[string]any   `json:"perception"`
	AvailableActions []map[string]any `json:"available_actions"`
	SystemPrompt     string           `json:"system_prompt,omitempty"`
	History          []historyEntry   `json:"history"`
}

// decideResponse is the JSON body returned by the agent provider.
type decideResponse struct {
	Actions []actionSpec `json:"actions"`
}

// actionSpec is one action in the response (or history).
type actionSpec struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// historyEntry is one past decision kept in the per-agent history buffer.
type historyEntry struct {
	Tick    int          `json:"tick"`
	Actions []actionSpec `json:"actions"`
	Result  string       `json:"result,omitempty"`
	Reason  string       `json:"reason,omitempty"`
}

// ---------------------------------------------------------------------------
// AgentDispatcher
// ---------------------------------------------------------------------------

const historyMaxLen = 10

// AgentDispatcher assembles perception, calls agent providers in parallel, and
// returns the collected actions as pendingActions injected into the tick queue.
type AgentDispatcher struct {
	world   *World
	client  *http.Client
	history map[string][]historyEntry // agentID → ring buffer
}

func newAgentDispatcher(w *World) *AgentDispatcher {
	return &AgentDispatcher{
		world:   w,
		client:  &http.Client{}, // timeout set per-request via context
		history: make(map[string][]historyEntry),
	}
}

// Decide dispatches all agent-backed entities for this tick in parallel and
// returns the resulting pendingActions. Non-agent entities are unaffected.
func (ad *AgentDispatcher) Decide(tick int) []pendingAction {
	w := ad.world

	// Collect agent entities that are due this tick.
	type agentTask struct {
		entityID string
		cfg      *AgentConfig
		provider ProviderConfig
	}

	var tasks []agentTask
	for id, e := range w.entities {
		cfg := w.registry.AgentConfig(e.typeName)
		if cfg == nil {
			continue
		}
		freq := cfg.TickFrequency
		if freq <= 0 {
			freq = 1
		}
		if tick%freq != 0 {
			continue
		}
		prov, ok := w.providers[cfg.Provider]
		if !ok {
			continue // provider not registered
		}
		tasks = append(tasks, agentTask{entityID: id, cfg: cfg, provider: prov})
	}

	if len(tasks) == 0 {
		return nil
	}

	// Dispatch all tasks in parallel.
	type result struct {
		actions []pendingAction
	}
	results := make(chan result, len(tasks))

	for _, task := range tasks {
		task := task
		go func() {
			actions := ad.callProvider(tick, task.entityID, task.cfg, task.provider)
			results <- result{actions: actions}
		}()
	}

	var all []pendingAction
	for range tasks {
		r := <-results
		all = append(all, r.actions...)
	}
	return all
}

// callProvider makes the HTTP POST /decide call for one agent and returns
// the resulting pendingActions.
func (ad *AgentDispatcher) callProvider(tick int, agentID string, cfg *AgentConfig, prov ProviderConfig) []pendingAction {
	perception := BuildPerception(ad.world, agentID, cfg)
	available := BuildAvailableActions(ad.world, agentID)
	history := ad.historyFor(agentID)

	req := decideRequest{
		AgentID:          agentID,
		Tick:             tick,
		Perception:       perception,
		AvailableActions: available,
		SystemPrompt:     cfg.Prompt,
		History:          history,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil
	}

	timeoutMs := prov.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10000
	}
	retries := prov.Retries
	if retries < 0 {
		retries = 0
	}

	var resp *decideResponse
	for attempt := 0; attempt <= retries; attempt++ {
		resp, err = ad.post(prov, body, timeoutMs)
		if err == nil {
			break
		}
	}
	if err != nil || resp == nil {
		ad.recordHistory(agentID, tick, nil, "timeout", "")
		return nil
	}

	// Convert response actions to pending actions.
	var actions []pendingAction
	for _, a := range resp.Actions {
		pa := pendingAction{
			invokerID: agentID,
			name:      a.Name,
			params:    P(a.Params),
		}
		if pa.params == nil {
			pa.params = P{}
		}
		actions = append(actions, pa)
	}

	ad.recordHistory(agentID, tick, resp.Actions, "ok", "")
	return actions
}

// post performs one HTTP POST with a per-request timeout.
func (ad *AgentDispatcher) post(prov ProviderConfig, body []byte, timeoutMs int) (*decideResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	endpoint := prov.Endpoint + "/decide"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if prov.AuthType == "bearer" && prov.TokenEnv != "" {
		token := os.Getenv(prov.TokenEnv)
		if token != "" {
			httpReq.Header.Set("Authorization", "Bearer "+token)
		}
	}

	httpResp, err := ad.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent provider HTTP error: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("agent provider returned status %d", httpResp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var resp decideResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("agent provider response parse error: %w", err)
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// History ring buffer
// ---------------------------------------------------------------------------

func (ad *AgentDispatcher) historyFor(agentID string) []historyEntry {
	h := ad.history[agentID]
	if h == nil {
		return []historyEntry{}
	}
	return h
}

func (ad *AgentDispatcher) recordHistory(agentID string, tick int, actions []actionSpec, result, reason string) {
	entry := historyEntry{Tick: tick, Actions: actions, Result: result, Reason: reason}
	h := ad.history[agentID]
	h = append(h, entry)
	if len(h) > historyMaxLen {
		h = h[len(h)-historyMaxLen:]
	}
	ad.history[agentID] = h
}
