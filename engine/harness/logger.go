package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogEntry is one line in the JSONL run log.
type LogEntry struct {
	Tick             int              `json:"tick"`
	AgentID          string           `json:"agent_id"`
	Perception       map[string]any   `json:"perception"`
	AvailableActions []map[string]any `json:"available_actions"`
	ReasoningText    string           `json:"reasoning_text"`
	ActionsTaken     []Action         `json:"actions_taken"`
	LatencyMs        int64            `json:"latency_ms"`
	FindingText      string           `json:"finding_text,omitempty"` // set if record_finding was called
	Notes            string           `json:"notes"`                  // accumulated notes at this tick
}

// RunLogger writes JSONL logs per agent per run.
type RunLogger struct {
	mu     sync.Mutex
	files  map[string]*os.File
	logDir string
	runID  string
}

// NewRunLogger creates a logger writing to logDir/runID/<agentID>.jsonl.
func NewRunLogger(logDir string) (*RunLogger, error) {
	runID := time.Now().UTC().Format("20060102-150405")
	dir := filepath.Join(logDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating log dir %s: %w", dir, err)
	}
	return &RunLogger{
		files:  make(map[string]*os.File),
		logDir: logDir,
		runID:  runID,
	}, nil
}

// RunDir returns the directory for this run's logs.
func (l *RunLogger) RunDir() string {
	return filepath.Join(l.logDir, l.runID)
}

// Write appends one entry to the agent's JSONL log.
func (l *RunLogger) Write(entry LogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := l.fileFor(entry.AgentID)
	if err != nil {
		return err
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshalling log entry: %w", err)
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

// Flush is a no-op (writes are unbuffered). Kept for interface compatibility.
func (l *RunLogger) Flush() error { return nil }

// Close closes all log files.
func (l *RunLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []error
	for _, f := range l.files {
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	l.files = make(map[string]*os.File)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (l *RunLogger) fileFor(agentID string) (*os.File, error) {
	if f, ok := l.files[agentID]; ok {
		return f, nil
	}
	path := filepath.Join(l.logDir, l.runID, agentID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening log file %s: %w", path, err)
	}
	l.files[agentID] = f
	return f, nil
}
