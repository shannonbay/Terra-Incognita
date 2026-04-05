package harness_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shannonbay/terra-incognita/engine/harness"
)

// mockClaudeHandler returns a mock Anthropic Messages API response.
// tick is extracted from the message content to vary the response.
type mockClaudeHandler struct {
	t          *testing.T
	callCount  int
	recordNote bool // whether to include a record_finding call in responses
}

func (m *mockClaudeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.callCount++

	// Parse request to decide what to return
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)

	// Determine if we should include record_finding (on first call only)
	includeRecordFinding := m.recordNote && m.callCount == 1

	w.Header().Set("Content-Type", "application/json")

	var content []map[string]any
	content = append(content, map[string]any{
		"type": "text",
		"text": fmt.Sprintf("Reasoning for call %d: I will take an action.", m.callCount),
	})

	if includeRecordFinding {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    "rf_001",
			"name":  "record_finding",
			"input": map[string]any{"text": "I have observed the world state."},
		})
	}

	content = append(content, map[string]any{
		"type": "tool_use",
		"id":   fmt.Sprintf("ta_%03d", m.callCount),
		"name": "take_actions",
		"input": map[string]any{
			"actions": []map[string]any{
				{"name": "do_nothing", "params": map[string]any{}},
			},
		},
	})

	resp := map[string]any{
		"id":           fmt.Sprintf("msg_%03d", m.callCount),
		"type":         "message",
		"role":         "assistant",
		"model":        "claude-sonnet-4-6",
		"content":      content,
		"stop_reason":  "tool_use",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  100,
			"output_tokens": 50,
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// setupHarness creates a test harness server backed by a mock Claude API.
func setupHarness(t *testing.T, maxHistoryTicks int, recordNote bool) (*httptest.Server, *mockClaudeHandler, string) {
	t.Helper()

	logDir := t.TempDir()

	mockHandler := &mockClaudeHandler{t: t, recordNote: recordNote}
	mockAPI := httptest.NewServer(mockHandler)
	t.Cleanup(mockAPI.Close)

	cfg := harness.DefaultConfig()
	cfg.MaxHistoryTicks = maxHistoryTicks
	cfg.LogDir = logDir
	cfg.MaxTokens = 1024

	llmClient := harness.NewLLMClientWithBaseURL(cfg, mockAPI.URL)
	srv, err := harness.NewWithLLM(cfg, llmClient)
	if err != nil {
		t.Fatalf("creating harness: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	hts := httptest.NewServer(srv)
	t.Cleanup(hts.Close)

	return hts, mockHandler, logDir
}

// postDecide sends a POST /decide request to the harness test server.
func postDecide(t *testing.T, serverURL string, agentID string, tick int) *harness.DecideResponse {
	t.Helper()

	req := harness.DecideRequest{
		AgentID: agentID,
		Tick:    tick,
		Perception: map[string]any{
			"food_pool": 300.0,
			"tick":      tick,
		},
		AvailableActions: []map[string]any{
			{"name": "do_nothing", "description": "Take no action."},
			{"name": "allocate", "description": "Allocate resources."},
		},
		SystemPrompt: "You are an agent in a world. Govern wisely.",
	}

	body, _ := json.Marshal(req)
	resp, err := http.Post(serverURL+"/decide", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /decide: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, b)
	}

	var dr harness.DecideResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return &dr
}

func TestHarness_BasicDecide(t *testing.T) {
	srv, mockHandler, logDir := setupHarness(t, 20, false)

	const agentID = "steward1"

	// Send 3 decide requests.
	for tick := 1; tick <= 3; tick++ {
		resp := postDecide(t, srv.URL, agentID, tick)
		if len(resp.Actions) == 0 {
			t.Errorf("tick %d: expected actions, got none", tick)
		}
		if resp.Actions[0].Name != "do_nothing" {
			t.Errorf("tick %d: expected do_nothing, got %s", tick, resp.Actions[0].Name)
		}
	}

	if mockHandler.callCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", mockHandler.callCount)
	}

	// Verify JSONL log was written with 3 entries.
	entries := readJSONLEntries(t, logDir, agentID)
	if len(entries) != 3 {
		t.Errorf("expected 3 log entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e["tick"] == nil {
			t.Errorf("entry %d missing tick field", i)
		}
		if e["reasoning_text"] == "" || e["reasoning_text"] == nil {
			t.Errorf("entry %d missing reasoning_text", i)
		}
	}
}

func TestHarness_RecordFinding(t *testing.T) {
	srv, _, logDir := setupHarness(t, 20, true) // mock will call record_finding on tick 1

	const agentID = "steward2"

	// Tick 1: mock includes record_finding call
	resp := postDecide(t, srv.URL, agentID, 1)
	if len(resp.Actions) == 0 {
		t.Fatal("tick 1: no actions returned")
	}

	// Tick 2: the user message should include the note from tick 1
	resp = postDecide(t, srv.URL, agentID, 2)
	if len(resp.Actions) == 0 {
		t.Fatal("tick 2: no actions returned")
	}

	// Verify log entry 1 has finding_text set
	entries := readJSONLEntries(t, logDir, agentID)
	if len(entries) < 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}
	if ft := entries[0]["finding_text"]; ft == nil || ft == "" {
		t.Error("entry 0 should have finding_text set (record_finding was called)")
	}
	// Entry 2 should have notes (accumulated from tick 1's finding)
	if notes := entries[1]["notes"]; notes == nil || notes == "" {
		t.Error("entry 1 should have notes (from tick 1's record_finding)")
	}
}

func TestHarness_HistoryTrim(t *testing.T) {
	// max_history_ticks=2 so after 3 ticks we should have trimmed tick 1.
	srv, _, logDir := setupHarness(t, 2, false)

	const agentID = "steward3"

	for tick := 1; tick <= 3; tick++ {
		postDecide(t, srv.URL, agentID, tick)
	}

	// Log should have 3 entries (trim doesn't affect logging).
	entries := readJSONLEntries(t, logDir, agentID)
	if len(entries) != 3 {
		t.Errorf("expected 3 log entries, got %d", len(entries))
	}

	// The 3rd entry's reasoning_text should have a compacted context clue.
	// We can't directly inspect the conversation in this black-box test,
	// but we can verify that all 3 calls succeeded without error.
	for i, e := range entries {
		if _, ok := e["tick"]; !ok {
			t.Errorf("entry %d missing tick", i)
		}
	}
}

// readJSONLEntries finds the JSONL file for agentID in logDir and returns parsed entries.
func readJSONLEntries(t *testing.T, logDir, agentID string) []map[string]any {
	t.Helper()

	// Find the run subdirectory (there should be exactly one).
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("reading log dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no run directory created in log dir")
	}

	runDir := filepath.Join(logDir, entries[0].Name())
	logPath := filepath.Join(runDir, agentID+".jsonl")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading log file %s: %v", logPath, err)
	}

	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parsing log line: %v\nline: %s", err, line)
		}
		result = append(result, entry)
	}
	return result
}
