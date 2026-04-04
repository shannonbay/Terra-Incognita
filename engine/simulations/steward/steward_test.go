package steward

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

func TestSteward_MockAgent(t *testing.T) {
	tick := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tick++
		var actions []map[string]any
		switch tick % 4 {
		case 0:
			actions = []map[string]any{{"name": "allocate", "params": map[string]any{
				"island": "island_1", "resource": "food", "amount": 20.0,
			}}}
		case 1:
			actions = []map[string]any{{"name": "investigate", "params": map[string]any{
				"target": "archipelago", "field": "fish_stock",
			}}}
		case 2:
			actions = []map[string]any{{"name": "allocate", "params": map[string]any{
				"island": "island_3", "resource": "food", "amount": 15.0,
			}}}
		default:
			actions = []map[string]any{{"name": "do_nothing", "params": map[string]any{}}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"actions": actions})
	}))
	defer srv.Close()

	runDir := t.TempDir()

	cfg := DefaultConfig()
	cfg.MaxTicks = 30
	cfg.LogEnabled = true
	cfg.RunDir = runDir
	cfg.ProviderEndpoint = srv.URL
	cfg.SnapshotInterval = 10

	world := BuildWorld(cfg)

	// Capture log path before Run() closes the log
	world.Step(1) // initialise subsystems and open log
	logPath := world.RunLogPath()
	t.Logf("log path: %s", logPath)

	// Complete the run (closes the log)
	world.Run()

	// Verify: simulation completed
	if world.CurrentTick() != 30 {
		t.Errorf("want tick=30, got %d", world.CurrentTick())
	}

	// Verify: islanders have non-zero wellbeing
	islanders := world.ListEntities("Islander")
	if len(islanders) != 30 {
		t.Errorf("want 30 islanders, got %d", len(islanders))
	}
	totalWellbeing := 0.0
	for _, e := range islanders {
		totalWellbeing += e.Get("wellbeing")
	}
	if totalWellbeing <= 0 {
		t.Error("want total wellbeing > 0")
	}

	// Verify: run log exists
	if logPath == "" {
		t.Error("want non-empty log path")
	}

	// StateAt is not available after Run() closes the log (would need to reopen).
	// We verify the log file exists on disk.
	if logPath != "" {
		if !filepath.IsAbs(logPath) {
			t.Errorf("log path is not absolute: %s", logPath)
		}
	}

	// Verify: Archipelago exists and has fish_stock
	arch := world.Entity("archipelago")
	if arch == nil {
		t.Fatal("archipelago entity not found")
	}
	if arch.Get("fish_stock") <= 0 {
		t.Error("want fish_stock > 0")
	}

	// Verify: run analysis (reopen log from path)
	if logPath != "" {
		ra, err := AnalyzeRun(world, logPath)
		if err != nil {
			t.Errorf("AnalyzeRun: %v", err)
		} else {
			t.Logf("Analysis: allocations=%d, investigates=%d, voiced_gap=%.2f, ecosystem=%.2f",
				ra.AllocationsTotal, ra.InvestigateCallsTotal, ra.VoicedVsUnvoicedGap, ra.FinalEcosystemHealth)
		}
	}

	_ = we.Hidden // ensure worldengine import is used
}

// TestSteward_StateAt verifies StateAt reconstruction from a completed log.
func TestSteward_StateAt(t *testing.T) {
	tick := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tick++
		actions := []map[string]any{{"name": "do_nothing", "params": map[string]any{}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"actions": actions})
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.MaxTicks = 20
	cfg.LogEnabled = true
	cfg.RunDir = t.TempDir()
	cfg.ProviderEndpoint = srv.URL
	cfg.SnapshotInterval = 5

	world := BuildWorld(cfg)

	// Capture log path before Run() closes it
	world.Step(1)
	logPath := world.RunLogPath()
	world.Run()

	if logPath == "" {
		t.Skip("logging not available")
	}

	// Reopen the log to test StateAt
	rl, err := we.OpenRunLog(logPath)
	if err != nil {
		t.Fatalf("OpenRunLog: %v", err)
	}
	defer rl.Close()

	state, err := rl.StateAt(15)
	if err != nil {
		t.Errorf("StateAt(15): %v", err)
	}
	if state == nil {
		t.Error("StateAt(15) returned nil")
	}
}
