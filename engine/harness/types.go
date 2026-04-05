package harness

// DecideRequest matches the worldengine agent_dispatcher wire format (spec §9.2).
type DecideRequest struct {
	AgentID          string           `json:"agent_id"`
	Tick             int              `json:"tick"`
	Perception       map[string]any   `json:"perception"`
	AvailableActions []map[string]any `json:"available_actions"`
	SystemPrompt     string           `json:"system_prompt,omitempty"`
	History          []HistoryEntry   `json:"history"`
}

// DecideResponse is the JSON body returned to the worldengine.
type DecideResponse struct {
	Actions []Action `json:"actions"`
}

// Action is one action in the response or history.
type Action struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// HistoryEntry is one past decision from the worldengine's history buffer.
type HistoryEntry struct {
	Tick    int      `json:"tick"`
	Actions []Action `json:"actions"`
	Result  string   `json:"result,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}
