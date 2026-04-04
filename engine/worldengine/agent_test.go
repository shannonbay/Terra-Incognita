package worldengine

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockAgentServer spins up an httptest.Server that returns the given actions.
// It captures the last received request for inspection.
type mockAgentServer struct {
	server  *httptest.Server
	lastReq atomic.Pointer[decideRequest]
	actions []actionSpec
	code    int // HTTP status code to return (0 = 200)
}

func newMockAgent(actions []actionSpec) *mockAgentServer {
	m := &mockAgentServer{actions: actions}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/decide" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req decideRequest
		_ = json.Unmarshal(body, &req)
		m.lastReq.Store(&req)

		code := m.code
		if code == 0 {
			code = 200
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		resp := decideResponse{Actions: m.actions}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return m
}

func (m *mockAgentServer) Close() { m.server.Close() }
func (m *mockAgentServer) URL() string { return m.server.URL }

// buildAgentWorld creates a world with one agent-backed entity registered to
// the given mock server.
func buildAgentWorld(t *testing.T, mock *mockAgentServer) *World {
	t.Helper()
	w := New(Config{MaxTicks: 3})

	lake := w.Type("Lake")
	lake.Resources(P{"fish_stock": 100.0})
	// "fish" is on the Lake (target) type — invoker is the boat that calls it.
	lake.Action("fish", func(target *Entity, invoker *Entity, p P) ActionResult {
		catch := target.Get("fish_stock") * 0.1
		invoker.Set("catch", invoker.Get("catch")+catch)
		target.Set("fish_stock", target.Get("fish_stock")-catch)
		return OK()
	})

	boat := w.Type("Boat")
	boat.Resources(P{"fuel": 50.0, "catch": 0.0})
	boat.Hidden("fuel") // fuel is hidden from other entities
	boat.Agent(AgentConfig{
		Provider:   "mock",
		Prompt:     "Catch as much fish as possible.",
		Perception: []string{"/self/location"},
	})

	w.Provider("mock", ProviderConfig{
		Endpoint:  mock.URL(),
		TimeoutMs: 2000,
	})

	w.Spawn("lake1", "Lake", Init{})
	w.Spawn("boat1", "Boat", Init{Location: "lake1"})

	return w
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAgent_RequestShape(t *testing.T) {
	mock := newMockAgent(nil)
	defer mock.Close()

	w := buildAgentWorld(t, mock)
	w.Step(1)

	req := mock.lastReq.Load()
	if req == nil {
		t.Fatal("no request received")
	}
	if req.AgentID != "boat1" {
		t.Errorf("want agent_id=boat1, got %q", req.AgentID)
	}
	if req.Tick != 0 {
		t.Errorf("want tick=0, got %d", req.Tick)
	}
	if req.SystemPrompt != "Catch as much fish as possible." {
		t.Errorf("wrong system_prompt: %q", req.SystemPrompt)
	}
	if _, ok := req.Perception["/self"]; !ok {
		t.Error("want /self in perception")
	}
	if _, ok := req.Perception["/world/config"]; !ok {
		t.Error("want /world/config in perception")
	}
	if len(req.AvailableActions) == 0 {
		t.Error("want available_actions to be non-empty")
	}
}

func TestAgent_ActionInjected(t *testing.T) {
	mock := newMockAgent([]actionSpec{{Name: "fish", Params: map[string]any{"target": "lake1"}}})
	defer mock.Close()

	w := buildAgentWorld(t, mock)
	w.Step(1)

	boat := w.Entity("boat1")
	if boat.Get("catch") == 0 {
		t.Error("fish action was not executed: catch is still 0")
	}
}

func TestAgent_HiddenResourceStripped(t *testing.T) {
	mock := newMockAgent(nil)
	defer mock.Close()

	// Add a second observer boat so that boat1 shows up in /self/location/contains.
	w := New(Config{MaxTicks: 1})
	lake := w.Type("Lake")
	lake.Resources(P{"fish_stock": 100.0})
	boat := w.Type("Boat")
	boat.Resources(P{"fuel": 50.0, "catch": 0.0})
	boat.Hidden("fuel")
	boat.Agent(AgentConfig{
		Provider:   "mock",
		Perception: []string{"/self/location/contains"},
	})
	w.Provider("mock", ProviderConfig{Endpoint: mock.URL(), TimeoutMs: 2000})

	w.Spawn("lake1", "Lake", Init{})
	w.Spawn("agent1", "Boat", Init{Location: "lake1"})
	w.Spawn("boat2", "Boat", Init{Location: "lake1"}) // peer

	w.Step(1)

	req := mock.lastReq.Load()
	if req == nil {
		t.Fatal("no request received")
	}

	// /self/location/contains should contain boat2 without its hidden fuel.
	containsRaw, ok := req.Perception["/self/location/contains"]
	if !ok {
		t.Fatal("want /self/location/contains in perception")
	}

	// Serialize/deserialize to check field presence.
	b, _ := json.Marshal(containsRaw)
	var peers []map[string]any
	_ = json.Unmarshal(b, &peers)

	for _, peer := range peers {
		if peer["id"] == "agent1" {
			continue // skip self
		}
		if res, ok := peer["resources"]; ok {
			resMap, _ := res.(map[string]any)
			if _, hasFuel := resMap["fuel"]; hasFuel {
				t.Error("hidden resource 'fuel' should not appear in peer's perception")
			}
		}
	}
}

func TestAgent_TimeoutProducesNoAction(t *testing.T) {
	// Server that is slower than the configured timeout.
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond) // much longer than 50 ms agent timeout
		w.WriteHeader(200)
	}))
	defer hung.Close()

	w := New(Config{MaxTicks: 1})
	bt := w.Type("Boat")
	bt.Resources(P{"catch": 0.0})
	bt.Action("fish", func(target *Entity, invoker *Entity, p P) ActionResult {
		invoker.Set("catch", 99.0)
		return OK()
	})
	bt.Agent(AgentConfig{Provider: "hung", Perception: []string{}})
	w.Provider("hung", ProviderConfig{
		Endpoint:  hung.URL,
		TimeoutMs: 50, // very short timeout
	})
	w.Spawn("b1", "Boat", Init{})

	w.Step(1)

	b1 := w.Entity("b1")
	if b1.Get("catch") != 0 {
		t.Error("timed-out agent should produce no actions, but catch was set")
	}
}

func TestAgent_HistoryAccumulates(t *testing.T) {
	mock := newMockAgent(nil)
	defer mock.Close()

	w := buildAgentWorld(t, mock)
	w.Step(3)

	req := mock.lastReq.Load()
	if req == nil {
		t.Fatal("no request received")
	}
	// After 3 ticks, history should have 2 entries (ticks 0 and 1; tick 2 is current).
	if len(req.History) < 2 {
		t.Errorf("want at least 2 history entries after 3 ticks, got %d", len(req.History))
	}
}

func TestAgent_TickFrequencySkips(t *testing.T) {
	var callCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decideResponse{})
	}))
	defer srv.Close()

	w := New(Config{MaxTicks: 6})
	bt := w.Type("Agent")
	bt.Resources(P{})
	bt.Agent(AgentConfig{
		Provider:      "srv",
		TickFrequency: 3, // called every 3 ticks: 0, 3
	})
	w.Provider("srv", ProviderConfig{Endpoint: srv.URL, TimeoutMs: 1000})
	w.Spawn("a1", "Agent", Init{})

	w.Run()

	// 6 ticks (0-5), frequency=3 → called at ticks 0 and 3 = 2 calls
	got := atomic.LoadInt64(&callCount)
	if got != 2 {
		t.Errorf("want 2 agent calls (ticks 0+3), got %d", got)
	}
}

func TestAgent_PerceptionSelfResources(t *testing.T) {
	mock := newMockAgent(nil)
	defer mock.Close()

	w := buildAgentWorld(t, mock)
	w.Step(1)

	req := mock.lastReq.Load()
	selfRaw, ok := req.Perception["/self"]
	if !ok {
		t.Fatal("want /self in perception")
	}
	b, _ := json.Marshal(selfRaw)
	var self map[string]any
	_ = json.Unmarshal(b, &self)
	if _, has := self["fuel"]; !has {
		t.Error("agent should see its own hidden 'fuel' resource in /self")
	}
}
