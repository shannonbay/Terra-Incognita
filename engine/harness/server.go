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
	config   HarnessConfig
	provider Provider
	logger   *RunLogger
	mux      *http.ServeMux
}

// New creates a Server using the provider specified in cfg.Provider.
// "api" (default) uses the Anthropic API directly (requires ANTHROPIC_API_KEY).
// "claude-code" uses the `claude` CLI subprocess (uses active claude auth).
func New(cfg HarnessConfig) (*Server, error) {
	var p Provider

	switch cfg.Provider {
	case "claude-code":
		p = newClaudeCodeProvider(cfg)
	default: // "api" or empty
		llm, err := NewLLMClient(cfg)
		if err != nil {
			return nil, err
		}
		p = newAPIProvider(cfg, llm)
	}

	return newWithProvider(cfg, p)
}

// NewWithLLM constructs a Server with a provided LLMClient (used in tests).
func NewWithLLM(cfg HarnessConfig, llm *LLMClient) (*Server, error) {
	return newWithProvider(cfg, newAPIProvider(cfg, llm))
}

// newWithProvider is the internal constructor used by both New and NewWithLLM.
func newWithProvider(cfg HarnessConfig, p Provider) (*Server, error) {
	logger, err := NewRunLogger(cfg.LogDir)
	if err != nil {
		return nil, fmt.Errorf("creating run logger: %w", err)
	}

	s := &Server{
		config:   cfg,
		provider: p,
		logger:   logger,
		mux:      http.NewServeMux(),
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

	log.Printf("harness listening on %s  provider=%s  model=%s  log=%s",
		ln.Addr(), s.config.Provider, s.config.Model, s.logger.RunDir())

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

// decide calls the provider and logs the result.
func (s *Server) decide(ctx context.Context, req DecideRequest) (*DecideResponse, error) {
	result, err := s.provider.Decide(ctx, req.AgentID, req)
	if err != nil {
		return nil, err
	}

	_ = s.logger.Write(LogEntry{
		Tick:             req.Tick,
		AgentID:          req.AgentID,
		Perception:       req.Perception,
		AvailableActions: req.AvailableActions,
		ReasoningText:    result.ReasoningText,
		ActionsTaken:     result.Actions,
		LatencyMs:        result.LatencyMs,
		FindingText:      result.FindingText,
		Notes:            s.provider.Notes(req.AgentID),
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
