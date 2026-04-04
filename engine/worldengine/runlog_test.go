package worldengine

import (
	"os"
	"path/filepath"
	"testing"
)

// buildLoggedWorld creates a small world with logging enabled, runs it for N
// ticks, and returns the run log path and the live world state for comparison.
func buildLoggedWorld(t *testing.T, ticks int) (logPath string, w *World) {
	t.Helper()
	dir := t.TempDir()

	w = New(Config{
		Name:     "test",
		MaxTicks: ticks,
		Log: LogConfig{
			Dir:              dir,
			SnapshotInterval: 50,
			Enabled:          true,
		},
	})

	lake := w.Type("Lake")
	lake.Resources(P{"fish_stock": 100.0})
	lake.Tick(func(e *Entity, dt float64) {
		stock := e.Get("fish_stock")
		e.Set("fish_stock", stock+1.0) // grows by 1 each tick
	})

	boat := w.Type("Boat")
	boat.Resources(P{"cargo": 0.0})
	boat.Tick(func(e *Entity, dt float64) {
		e.Set("cargo", e.Get("cargo")+0.5)
	})

	w.Spawn("lake1", "Lake", Init{Resources: P{"fish_stock": 10.0}})
	w.Spawn("lake2", "Lake", Init{Resources: P{"fish_stock": 20.0}})
	w.Spawn("boat1", "Boat", Init{Resources: P{"cargo": 0.0}, Location: "lake1"})
	w.Connect("lake1", "lake2", "route", 1.0)

	w.Run()

	// Find the generated .db file.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no db file created in %s: %v", dir, err)
	}
	logPath = filepath.Join(dir, entries[0].Name())
	return logPath, w
}

// ---------------------------------------------------------------------------
// Basic metadata and schema
// ---------------------------------------------------------------------------

func TestRunLog_MetaWritten(t *testing.T) {
	logPath, _ := buildLoggedWorld(t, 10)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	meta, err := rl.Meta()
	if err != nil {
		t.Fatal(err)
	}
	if meta.WorldName != "test" {
		t.Errorf("want world_name=test, got %q", meta.WorldName)
	}
	if meta.Status != "completed" {
		t.Errorf("want status=completed, got %q", meta.Status)
	}
	if meta.FinalTick != "10" {
		t.Errorf("want final_tick=10, got %q", meta.FinalTick)
	}
}

// ---------------------------------------------------------------------------
// Event counts
// ---------------------------------------------------------------------------

func TestRunLog_ResourceSetEventCount(t *testing.T) {
	const ticks = 20
	logPath, _ := buildLoggedWorld(t, ticks)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	evts, err := rl.Events(EventQuery{EventType: "resource_set"})
	if err != nil {
		t.Fatal(err)
	}
	// lake1 + lake2 + boat1 each produce 1 resource_set per tick = 3*20 = 60
	if len(evts) != 3*ticks {
		t.Errorf("want %d resource_set events, got %d", 3*ticks, len(evts))
	}
}

func TestRunLog_EventsFilterByEntity(t *testing.T) {
	logPath, _ := buildLoggedWorld(t, 10)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	evts, err := rl.Events(EventQuery{EntityID: "boat1", EventType: "resource_set"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evts) != 10 {
		t.Errorf("want 10 boat1 resource_set events, got %d", len(evts))
	}
}

func TestRunLog_EventsTickRange(t *testing.T) {
	logPath, _ := buildLoggedWorld(t, 20)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	// ticks 5-9 inclusive = 5 ticks, 3 entities = 15 events
	evts, err := rl.Events(EventQuery{TickFrom: 5, TickTo: 9, EventType: "resource_set"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evts) != 15 {
		t.Errorf("want 15 events for ticks 5-9, got %d", len(evts))
	}
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

func TestRunLog_SnapshotAtTick0(t *testing.T) {
	logPath, _ := buildLoggedWorld(t, 10)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	var count int
	row := rl.db.QueryRow(`SELECT COUNT(*) FROM snapshots WHERE tick = 0`)
	if err := row.Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("want 1 tick-0 snapshot, got %d", count)
	}
}

func TestRunLog_PeriodicSnapshot(t *testing.T) {
	// SnapshotInterval=50, run 100 ticks → snapshots at tick 0 and tick 50
	// (tick 100 is not written because snap is written after tick 49 completes → tick 50)
	logPath, _ := buildLoggedWorld(t, 100)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	rows, err := rl.db.Query(`SELECT tick FROM snapshots ORDER BY tick`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ticks []int
	for rows.Next() {
		var tick int
		rows.Scan(&tick)
		ticks = append(ticks, tick)
	}
	if len(ticks) < 2 {
		t.Errorf("want at least 2 snapshots (tick 0 + tick 50), got %v", ticks)
	}
}

// ---------------------------------------------------------------------------
// State reconstruction
// ---------------------------------------------------------------------------

func TestRunLog_StateAtTick0(t *testing.T) {
	logPath, _ := buildLoggedWorld(t, 10)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	ws, err := rl.StateAt(0)
	if err != nil {
		t.Fatal(err)
	}
	lake1 := ws.Entity("lake1")
	if lake1 == nil {
		t.Fatal("lake1 not in reconstructed state at tick 0")
	}
	// At tick 0 (snapshot before any ticks), fish_stock should be the initial value.
	if lake1.Get("fish_stock") != 10.0 {
		t.Errorf("want fish_stock=10 at tick 0, got %v", lake1.Get("fish_stock"))
	}
}

func TestRunLog_StateAtTick5(t *testing.T) {
	logPath, _ := buildLoggedWorld(t, 10)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	ws, err := rl.StateAt(5)
	if err != nil {
		t.Fatal(err)
	}
	lake1 := ws.Entity("lake1")
	if lake1 == nil {
		t.Fatal("lake1 not in reconstructed state at tick 5")
	}
	// StateAt(T) returns state AFTER tick T has run.
	// lake1 starts at 10; tick function sets fish_stock = old+1 each tick.
	// After tick 5: 10 + 6 = 16  (ticks 0,1,2,3,4,5 each add 1).
	if lake1.Get("fish_stock") != 16.0 {
		t.Errorf("want fish_stock=16 after tick 5, got %v", lake1.Get("fish_stock"))
	}
}

func TestRunLog_StateAtMatchesLive(t *testing.T) {
	// Run a 200-tick simulation and compare reconstructed state at tick 100/150
	// against values computed from the same deterministic formula.
	const ticks = 200
	logPath, _ := buildLoggedWorld(t, ticks)
	rl, err := OpenRunLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	for _, checkTick := range []int{50, 100, 150} {
		ws, err := rl.StateAt(checkTick)
		if err != nil {
			t.Fatalf("StateAt(%d): %v", checkTick, err)
		}
		lake1 := ws.Entity("lake1")
		if lake1 == nil {
			t.Fatalf("tick %d: lake1 missing", checkTick)
		}
		// lake1 starts at 10 and grows +1 per tick
		want := 10.0 + float64(checkTick)
		got := lake1.Get("fish_stock")
		if got != want {
			t.Errorf("tick %d: want fish_stock=%v, got %v", checkTick, want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Spawn / destroy events
// ---------------------------------------------------------------------------

func TestRunLog_SpawnDestroyEvents(t *testing.T) {
	dir := t.TempDir()
	w := New(Config{
		Name:     "lifecycle",
		MaxTicks: 5,
		Log:      LogConfig{Dir: dir, SnapshotInterval: 100, Enabled: true},
	})

	at := w.Type("Actor")
	at.Resources(P{"hp": 3.0})
	at.Tick(func(e *Entity, dt float64) {
		// Spawn a child on the very first tick (tick 0) if not already spawned.
		if e.ID() == "a1" && !e.SetHas("spawned", "yes") {
			e.Spawn("a2", "Actor", Init{Resources: P{"hp": 3.0}})
			e.SetAdd("spawned", "yes")
		}
		e.Set("hp", e.Get("hp")-1.0)
		if e.Get("hp") <= 0 {
			e.DestroySelf()
		}
	})

	w.Spawn("a1", "Actor", Init{Resources: P{"hp": 3.0}})
	w.Run()

	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("no db created")
	}
	rl, _ := OpenRunLog(filepath.Join(dir, entries[0].Name()))
	defer rl.Close()

	spawned, _ := rl.Events(EventQuery{EventType: "entity_spawned"})
	if len(spawned) == 0 {
		t.Error("want at least 1 entity_spawned event")
	}
	destroyed, _ := rl.Events(EventQuery{EventType: "entity_destroyed"})
	if len(destroyed) == 0 {
		t.Error("want at least 1 entity_destroyed event")
	}
}
