package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
)

// Server is the harness HTTP server bridging worldengine and an LLM provider.
type Server struct {
	config HarnessConfig
	conv   *ConversationManager
	llm    *LLMClient
	logger *RunLogger
	mux    *http.ServeMux
}

// New creates a Server with a real Claude API client.
func New(cfg HarnessConfig) (*Server, error) {
	llm, err := NewLLMClient(cfg)
	if err != nil {
		return nil, err
	}
	return NewWithLLM(cfg, llm)
}

// NewWithLLM constructs a Server with a provided LLMClient (used in tests).
func NewWithLLM(cfg HarnessConfig, llm *LLMClient) (*Server, error) {
	logger, err := NewRunLogger(cfg.LogDir)
	if err != nil {
		return nil, fmt.Errorf("creating run logger: %w", err)
	}

	s := &Server{
		config: cfg,
		conv:   NewConversationManager(cfg.MaxHistoryTicks),
		llm:    llm,
		logger: logger,
		mux:    http.NewServeMux(),
	}
	s.mux.HandleFunc("/decide", s.handleDecide)
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.config.Port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	log.Printf("harness listening on %s  model=%s  log=%s", ln.Addr(), s.config.Model, s.logger.RunDir())

	srv := &http.Server{Handler: s}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		_ = s.logger.Flush()
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

// Flush flushes all buffered log data.
func (s *Server) Flush() error {
	return s.logger.Flush()
}

// Close flushes and closes all log files.
func (s *Server) Close() error {
	return s.logger.Close()
}

// handleDecide processes POST /decide from worldengine.
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "reading body", http.StatusBadRequest)
		return
	}

	var req DecideRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	resp, err := s.decide(ctx, req)
	if err != nil {
		log.Printf("decide error agent=%s tick=%d: %v", req.AgentID, req.Tick, err)
		resp = &DecideResponse{Actions: doNothingFallback(req.AvailableActions)}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// decide is the core logic: conversation → LLM → log → actions.
func (s *Server) decide(ctx context.Context, req DecideRequest) (*DecideResponse, error) {
	agentID := req.AgentID

	// Append the new perception to conversation history.
	s.conv.AppendUser(agentID, req.Tick, req.Perception, req.AvailableActions)

	// Build full message list for the LLM.
	messages := s.conv.Messages(agentID)

	// Call the LLM.
	result, err := s.llm.Decide(ctx, req.SystemPrompt, messages)
	if err != nil {
		return nil, err
	}

	// Persist any record_finding note.
	if result.FindingText != "" {
		s.conv.AppendFinding(agentID, result.FindingText)
	}

	// Append assistant turn + tool results to history.
	if result.RawResponse != nil {
		assistantMsg := result.RawResponse.ToParam()
		toolResults := BuildToolResults(result.RawResponse)
		s.conv.AppendAssistantTurn(agentID, assistantMsg, toolResults)
	}

	// Write the log entry.
	_ = s.logger.Write(LogEntry{
		Tick:             req.Tick,
		AgentID:          agentID,
		Perception:       req.Perception,
		AvailableActions: req.AvailableActions,
		ReasoningText:    result.ReasoningText,
		ActionsTaken:     result.Actions,
		LatencyMs:        result.LatencyMs,
		FindingText:      result.FindingText,
		Notes:            s.conv.Notes(agentID),
	})

	actions := result.Actions
	if actions == nil {
		actions = doNothingFallback(req.AvailableActions)
	}

	return &DecideResponse{Actions: actions}, nil
}

// doNothingFallback returns a do_nothing action if available in the action list.
func doNothingFallback(available []map[string]any) []Action {
	for _, a := range available {
		if name, _ := a["name"].(string); name == "do_nothing" {
			return []Action{{Name: "do_nothing"}}
		}
	}
	return []Action{}
}
