package worldengine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildScoringWorld creates a simple world with one agent and one resource,
// with a terminal score function that returns the agent's "value" resource.
func buildScoringWorld(mock *mockAgentServer) *World {
	w := New(Config{MaxTicks: 5})
	bt := w.Type("Bot")
	bt.Resources(P{"value": 0.0})
	bt.Agent(AgentConfig{Provider: "mock", Perception: []string{}})
	w.Provider("mock", ProviderConfig{Endpoint: mock.URL(), TimeoutMs: 500})
	w.Spawn("bot1", "Bot", Init{})

	// Each tick the bot gains 1 value via tick function.
	bt.Tick(func(e *Entity, dt float64) {
		e.Set("value", e.Get("value")+1)
	})

	// Terminal score = agent's value at end of run.
	w.Score(func(w *World, agentID string) float64 {
		return w.Entity(agentID).Get("value")
	})

	return w
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestScoring_TerminalScore(t *testing.T) {
	mock := newMockAgent(nil)
	defer mock.Close()

	w := buildScoringWorld(mock)
	w.Run()

	score := w.FinalScore("bot1")
	// 5 ticks × +1 per tick = 5
	if score != 5 {
		t.Errorf("want terminal score=5, got %.1f", score)
	}
}

func TestScoring_ContinuousScoreMean(t *testing.T) {
	mock := newMockAgent(nil)
	defer mock.Close()

	w := New(Config{MaxTicks: 4})
	bt := w.Type("Bot")
	bt.Resources(P{"value": 0.0})
	bt.Agent(AgentConfig{Provider: "mock", Perception: []string{}})
	bt.Tick(func(e *Entity, dt float64) {
		e.Set("value", e.Get("value")+1)
	})
	w.Provider("mock", ProviderConfig{Endpoint: mock.URL(), TimeoutMs: 500})
	w.Spawn("bot1", "Bot", Init{})

	// Continuous score = value at each tick (1, 2, 3, 4). Mean = 2.5.
	w.ScoreContinuous(func(w *World, agentID string, tick int) float64 {
		return w.Entity(agentID).Get("value")
	}, AggregateMean)

	w.Run()

	score := w.FinalScore("bot1")
	// mean of [1,2,3,4] = 2.5
	if score < 2.4 || score > 2.6 {
		t.Errorf("want continuous mean score≈2.5, got %.4f", score)
	}
}

func TestScoring_HiddenHidesFromPerception(t *testing.T) {
	var lastReq atomic.Pointer[decideRequest]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req decideRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastReq.Store(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decideResponse{})
	}))
	defer srv.Close()

	world := New(Config{MaxTicks: 1})
	bt := world.Type("Bot")
	bt.Resources(P{"value": 0.0})
	bt.Agent(AgentConfig{Provider: "mock", Perception: []string{}})
	world.Provider("mock", ProviderConfig{Endpoint: srv.URL, TimeoutMs: 500})
	world.Spawn("bot1", "Bot", Init{})

	world.Score(func(w *World, agentID string) float64 { return 42 })
	world.ScoreVisibility(Hidden)
	world.Run()

	req := lastReq.Load()
	if req == nil {
		t.Fatal("no request received")
	}
	if _, ok := req.Perception["/score/current"]; ok {
		t.Error("Hidden: /score/current should not appear in perception")
	}
	if _, ok := req.Perception["/score/hint"]; ok {
		t.Error("Hidden: /score/hint should not appear in perception")
	}
}

func TestScoring_HintsSendsGoalString(t *testing.T) {
	var lastReq atomic.Pointer[decideRequest]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req decideRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastReq.Store(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decideResponse{})
	}))
	defer srv.Close()

	world := New(Config{MaxTicks: 1})
	bt := world.Type("Bot")
	bt.Resources(P{"value": 0.0})
	bt.Agent(AgentConfig{Provider: "mock", Perception: []string{}})
	world.Provider("mock", ProviderConfig{Endpoint: srv.URL, TimeoutMs: 500})
	world.Spawn("bot1", "Bot", Init{})

	world.ScoreHint("Maximize value while staying efficient")
	world.Run()

	req := lastReq.Load()
	if req == nil {
		t.Fatal("no request received")
	}
	hint, ok := req.Perception["/score/hint"]
	if !ok {
		t.Fatal("Hints: want /score/hint in perception")
	}
	if hint != "Maximize value while staying efficient" {
		t.Errorf("wrong hint text: %v", hint)
	}
}

func TestTournament_LeaderboardOrdering(t *testing.T) {
	// Agent A: always does something (returns a "grow" action that sets value=10)
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := decideResponse{Actions: []actionSpec{{Name: "grow", Params: map[string]any{}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srvA.Close()

	// Agent B: does nothing
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decideResponse{})
	}))
	defer srvB.Close()

	worldFn := func() *World {
		w := New(Config{MaxTicks: 3})
		// Room holds the "grow" action (target); bot is the invoker inside it.
		room := w.Type("Room")
		room.Resources(P{})
		room.Action("grow", func(target *Entity, invoker *Entity, p P) ActionResult {
			invoker.Set("value", 10.0)
			return OK()
		})
		bt := w.Type("Bot")
		bt.Resources(P{"value": 0.0})
		bt.Agent(AgentConfig{Provider: "player", Perception: []string{}})
		w.Spawn("room1", "Room", Init{})
		w.Spawn("bot1", "Bot", Init{Location: "room1"})

		// Terminal score = value
		w.Score(func(w *World, agentID string) float64 {
			return w.Entity(agentID).Get("value")
		})
		return w
	}

	tr := NewTournament(TournamentConfig{
		Name:         "test",
		RunsPerWorld: 3,
		Aggregation:  AggregateMean,
	})
	tr.AddWorld("world1", worldFn)
	tr.AddWorld("world2", worldFn)
	tr.AddAgent("agent-a", ProviderConfig{Endpoint: srvA.URL, TimeoutMs: 500})
	tr.AddAgent("agent-b", ProviderConfig{Endpoint: srvB.URL, TimeoutMs: 500})

	results := tr.Run()

	if len(results.Leaderboard) != 2 {
		t.Fatalf("want 2 leaderboard entries, got %d", len(results.Leaderboard))
	}
	if results.Leaderboard[0].AgentName != "agent-a" {
		t.Errorf("want agent-a ranked #1, got %q (score=%.2f vs %.2f)",
			results.Leaderboard[0].AgentName,
			results.Leaderboard[0].Score,
			results.Leaderboard[1].Score)
	}
	if results.Leaderboard[0].Score <= results.Leaderboard[1].Score {
		t.Errorf("agent-a score (%.2f) should exceed agent-b score (%.2f)",
			results.Leaderboard[0].Score, results.Leaderboard[1].Score)
	}
}
