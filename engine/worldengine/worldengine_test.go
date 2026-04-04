package worldengine_test

import (
	"testing"

	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

// ----------------------------------------------------------------------------
// Helper
// ----------------------------------------------------------------------------

func newTestWorld() *we.World {
	return we.New(we.Config{DT: 1.0, MaxTicks: 10})
}

// ----------------------------------------------------------------------------
// TypeRegistry / TypeDef
// ----------------------------------------------------------------------------

func TestTypeRegistration(t *testing.T) {
	w := newTestWorld()
	_ = w.Type("Boat")
	_ = w.Type("Port")

	// Duplicate registration should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate type registration")
		}
	}()
	_ = w.Type("Boat")
}

func TestTypeDef_ParamsAndResources(t *testing.T) {
	w := newTestWorld()
	boat := w.Type("Boat")
	boat.Params(we.P{"speed": 5.0, "capacity": 20.0})
	boat.Resources(we.P{"fuel": 100.0, "catch": 0.0})

	e := w.Spawn("b1", "Boat", we.Init{})

	if got := e.Param("speed"); got != 5.0 {
		t.Errorf("Param speed: got %v, want 5.0", got)
	}
	if got := e.Get("fuel"); got != 100.0 {
		t.Errorf("Get fuel: got %v, want 100.0", got)
	}
}

func TestTypeDef_InitOverridesDefaults(t *testing.T) {
	w := newTestWorld()
	boat := w.Type("Boat")
	boat.Resources(we.P{"fuel": 100.0})
	boat.Params(we.P{"speed": 5.0})

	e := w.Spawn("b1", "Boat", we.Init{
		Resources: we.P{"fuel": 42.0},
		Params:    we.P{"speed": 9.0},
	})

	if got := e.Get("fuel"); got != 42.0 {
		t.Errorf("fuel: got %v, want 42.0", got)
	}
	if got := e.Param("speed"); got != 9.0 {
		t.Errorf("speed: got %v, want 9.0", got)
	}
}

// ----------------------------------------------------------------------------
// Entity — scalar resource access
// ----------------------------------------------------------------------------

func TestEntity_GetSet(t *testing.T) {
	w := newTestWorld()
	w.Type("Box").Resources(we.P{"x": 0.0})
	e := w.Spawn("box1", "Box", we.Init{})

	e.Set("x", 42.0)
	if got := e.Get("x"); got != 42.0 {
		t.Errorf("Get after Set: got %v, want 42.0", got)
	}
}

func TestEntity_GetOr(t *testing.T) {
	w := newTestWorld()
	w.Type("Box").Resources(we.P{"x": 0.0})
	e := w.Spawn("box1", "Box", we.Init{})

	// Existing resource
	if got := e.GetOr("x", 99.0); got != 0.0 {
		t.Errorf("GetOr existing: got %v, want 0.0", got)
	}
	// Missing resource
	if got := e.GetOr("money", 99.0); got != 99.0 {
		t.Errorf("GetOr missing: got %v, want 99.0", got)
	}
}

func TestEntity_Has(t *testing.T) {
	w := newTestWorld()
	w.Type("Box").Resources(we.P{"x": 0.0})
	e := w.Spawn("box1", "Box", we.Init{})

	if !e.Has("x") {
		t.Error("Has: expected true for existing resource")
	}
	if e.Has("y") {
		t.Error("Has: expected false for missing resource")
	}
}

func TestEntity_GetPanicsOnMissing(t *testing.T) {
	w := newTestWorld()
	w.Type("Box")
	e := w.Spawn("box1", "Box", we.Init{})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on Get for missing resource")
		}
	}()
	_ = e.Get("nonexistent")
}

// ----------------------------------------------------------------------------
// Entity — complex resource types
// ----------------------------------------------------------------------------

func TestEntity_List(t *testing.T) {
	w := newTestWorld()
	w.Type("Ship").Resources(we.P{"cargo": []any{}})
	e := w.Spawn("s1", "Ship", we.Init{})

	e.ListPush("cargo", "fish")
	e.ListPush("cargo", "gold")

	item := e.ListPop("cargo")
	if item != "gold" {
		t.Errorf("ListPop: got %v, want gold", item)
	}
}

func TestEntity_Queue(t *testing.T) {
	w := newTestWorld()
	w.Type("Station").Resources(we.P{"arrivals": we.Queue{}})
	e := w.Spawn("st1", "Station", we.Init{})

	e.QueuePush("arrivals", "train_1")
	e.QueuePush("arrivals", "train_2")

	first := e.QueuePop("arrivals")
	if first != "train_1" {
		t.Errorf("QueuePop: got %v, want train_1", first)
	}
}

func TestEntity_Set_resource(t *testing.T) {
	w := newTestWorld()
	w.Type("Node").Resources(we.P{"visited": we.NewSet()})
	e := w.Spawn("n1", "Node", we.Init{})

	e.SetAdd("visited", "port_alpha")
	if !e.SetHas("visited", "port_alpha") {
		t.Error("SetHas: expected true after SetAdd")
	}
	if e.SetHas("visited", "port_beta") {
		t.Error("SetHas: expected false for absent item")
	}
}

func TestEntity_Map(t *testing.T) {
	w := newTestWorld()
	w.Type("Market").Resources(we.P{"prices": map[string]any{}})
	e := w.Spawn("m1", "Market", we.Init{})

	e.MapSet("prices", "fish", 12.5)
	got := e.MapGet("prices", "fish")
	if got != 12.5 {
		t.Errorf("MapGet: got %v, want 12.5", got)
	}
	if e.MapGet("prices", "gold") != nil {
		t.Error("MapGet missing key: expected nil")
	}
}

// ----------------------------------------------------------------------------
// Visibility — TypeRegistry
// ----------------------------------------------------------------------------

func TestVisibility(t *testing.T) {
	w := newTestWorld()
	ground := w.Type("FishingGround")
	ground.Resources(we.P{"fish_stock": 100.0, "internal_id": 42.0, "name": 0.0})
	ground.Hidden("fish_stock")
	ground.Private("internal_id")

	// We test visibility via the registry directly (engine uses this in Phase 6)
	// For now, assert the TypeDef has the correct visibility stored by creating a
	// small world and checking via Spawn/Get (all resources readable from Go).
	e := w.Spawn("lake1", "FishingGround", we.Init{})
	if got := e.Get("fish_stock"); got != 100.0 {
		t.Errorf("hidden resource readable from Go: got %v, want 100.0", got)
	}
	if got := e.Get("internal_id"); got != 42.0 {
		t.Errorf("private resource readable from Go: got %v, want 42.0", got)
	}
}

// ----------------------------------------------------------------------------
// Delta capture
// ----------------------------------------------------------------------------

func TestDeltaCapture(t *testing.T) {
	w := newTestWorld()
	w.Type("Box").Resources(we.P{"x": 0.0})
	e := w.Spawn("box1", "Box", we.Init{})

	e.Set("x", 7.0)
	e.Set("x", 14.0)

	deltas := w.FlushDeltas()
	if len(deltas) < 2 {
		t.Fatalf("expected at least 2 deltas, got %d", len(deltas))
	}

	// Second delta: 7 → 14
	last := deltas[len(deltas)-1]
	if last.Field != "x" {
		t.Errorf("delta field: got %v, want x", last.Field)
	}
	if last.OldValue.(float64) != 7.0 {
		t.Errorf("delta old: got %v, want 7.0", last.OldValue)
	}
	if last.NewValue.(float64) != 14.0 {
		t.Errorf("delta new: got %v, want 14.0", last.NewValue)
	}
}

// ----------------------------------------------------------------------------
// Containment
// ----------------------------------------------------------------------------

func TestContainment(t *testing.T) {
	w := newTestWorld()
	w.Type("Lake")
	w.Type("Boat").Resources(we.P{"fuel": 50.0})

	w.Spawn("lake1", "Lake", we.Init{})
	w.Spawn("boat1", "Boat", we.Init{Location: "lake1"})
	w.Spawn("boat2", "Boat", we.Init{Location: "lake1"})

	lake := w.Entity("lake1")
	contained := lake.Contains()
	if contained.Count() != 2 {
		t.Errorf("Contains count: got %d, want 2", contained.Count())
	}

	boat1 := w.Entity("boat1")
	loc := boat1.Location()
	if loc == nil || loc.ID() != "lake1" {
		t.Errorf("Location: got %v, want lake1", loc)
	}
}

// ----------------------------------------------------------------------------
// Connection graph
// ----------------------------------------------------------------------------

func TestNeighbors(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})
	w.Spawn("c", "Node", we.Init{})
	w.Connect("a", "b", "road", 1.0)
	w.Connect("a", "c", "sea", 10.0)

	a := w.Entity("a")
	neighbors := a.Neighbors()
	if neighbors.Count() != 2 {
		t.Errorf("Neighbors count: got %d, want 2", neighbors.Count())
	}

	// Nearest should be b (weight 1.0 vs 10.0)
	nearest := neighbors.Nearest()
	if nearest == nil || nearest.ID() != "b" {
		t.Errorf("Nearest: got %v, want b", nearest)
	}
}

func TestNeighbors_TypeFilter(t *testing.T) {
	w := newTestWorld()
	w.Type("Port")
	w.Type("Lake")
	w.Spawn("port1", "Port", we.Init{})
	w.Spawn("lake1", "Lake", we.Init{})
	w.Spawn("boat", "Port", we.Init{}) // using Port type for simplicity
	w.Connect("boat", "port1", "shore", 1.0)
	w.Connect("boat", "lake1", "water", 2.0)

	boat := w.Entity("boat")
	ports := boat.Neighbors(we.Filter{Type: "Port"})
	if ports.Count() != 1 || ports.Entities()[0].ID() != "port1" {
		t.Errorf("Neighbors type filter: got %d entities", ports.Count())
	}
}

// ----------------------------------------------------------------------------
// EntitySet operations
// ----------------------------------------------------------------------------

func TestEntitySet_MaxByMinBy(t *testing.T) {
	w := newTestWorld()
	w.Type("Boat").Resources(we.P{"fuel": 0.0})
	w.Spawn("b1", "Boat", we.Init{Resources: we.P{"fuel": 10.0}})
	w.Spawn("b2", "Boat", we.Init{Resources: we.P{"fuel": 50.0}})
	w.Spawn("b3", "Boat", we.Init{Resources: we.P{"fuel": 30.0}})
	w.Connect("hub", "b1", "link", 1.0)
	w.Connect("hub", "b2", "link", 1.0)
	w.Connect("hub", "b3", "link", 1.0)

	// Build an EntitySet manually for testing
	set := we.NewEntitySet([]*we.Entity{w.Entity("b1"), w.Entity("b2"), w.Entity("b3")})

	if max := set.MaxBy("fuel"); max.ID() != "b2" {
		t.Errorf("MaxBy fuel: got %v, want b2", max.ID())
	}
	if min := set.MinBy("fuel"); min.ID() != "b1" {
		t.Errorf("MinBy fuel: got %v, want b1", min.ID())
	}
}

func TestEntitySet_SumAvg(t *testing.T) {
	w := newTestWorld()
	w.Type("Boat").Resources(we.P{"catch": 0.0})
	w.Spawn("b1", "Boat", we.Init{Resources: we.P{"catch": 10.0}})
	w.Spawn("b2", "Boat", we.Init{Resources: we.P{"catch": 20.0}})

	set := we.NewEntitySet([]*we.Entity{w.Entity("b1"), w.Entity("b2")})
	if got := set.Sum("catch"); got != 30.0 {
		t.Errorf("Sum: got %v, want 30.0", got)
	}
	if got := set.Avg("catch"); got != 15.0 {
		t.Errorf("Avg: got %v, want 15.0", got)
	}
}

// ----------------------------------------------------------------------------
// ActionResult
// ----------------------------------------------------------------------------

func TestActionResult(t *testing.T) {
	ok := we.OK()
	if !ok.IsOK() {
		t.Error("OK().IsOK(): expected true")
	}
	if ok.Reason() != "" {
		t.Errorf("OK().Reason(): expected empty, got %q", ok.Reason())
	}

	fail := we.Fail("out of fuel")
	if fail.IsOK() {
		t.Error("Fail().IsOK(): expected false")
	}
	if fail.Reason() != "out of fuel" {
		t.Errorf("Fail().Reason(): got %q, want %q", fail.Reason(), "out of fuel")
	}
}

// ----------------------------------------------------------------------------
// P helper methods
// ----------------------------------------------------------------------------

// ----------------------------------------------------------------------------
// Phase 3 — Graph tests
// ----------------------------------------------------------------------------

func TestGraph_Bidirectional(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})
	w.Connect("a", "b", "road", 5.0)

	// Both directions should exist
	aNeighbors := w.Entity("a").Neighbors()
	bNeighbors := w.Entity("b").Neighbors()

	if aNeighbors.Count() != 1 || aNeighbors.Entities()[0].ID() != "b" {
		t.Errorf("bidirectional a→b: got %d neighbors", aNeighbors.Count())
	}
	if bNeighbors.Count() != 1 || bNeighbors.Entities()[0].ID() != "a" {
		t.Errorf("bidirectional b→a: got %d neighbors", bNeighbors.Count())
	}
}

func TestGraph_Directed(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("src", "Node", we.Init{})
	w.Spawn("dst", "Node", we.Init{})
	w.ConnectDirected("src", "dst", "river", 1.0)

	srcNeighbors := w.Entity("src").Neighbors()
	dstNeighbors := w.Entity("dst").Neighbors()

	if srcNeighbors.Count() != 1 {
		t.Errorf("directed src→dst: got %d from src", srcNeighbors.Count())
	}
	if dstNeighbors.Count() != 0 {
		t.Errorf("directed src→dst: got %d from dst, want 0", dstNeighbors.Count())
	}
}

func TestGraph_RemoveEdge(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})
	w.Connect("a", "b", "road", 1.0)

	// Queue and apply disconnect via entity
	a := w.Entity("a")
	a.Disconnect("b", "road")

	// Process the pending disconnects manually (tick engine does this in Phase 4)
	w.ApplyPendingConnectChanges()

	if w.Entity("a").Neighbors().Count() != 0 {
		t.Error("RemoveEdge: expected 0 neighbors after disconnect")
	}
	if w.Entity("b").Neighbors().Count() != 0 {
		t.Error("RemoveEdge: expected bidirectional removal")
	}
}

func TestGraph_TypeFilteredTraversal(t *testing.T) {
	w := newTestWorld()
	w.Type("Port")
	w.Type("Lake")
	w.Spawn("hub", "Port", we.Init{})
	w.Spawn("p1", "Port", we.Init{})
	w.Spawn("l1", "Lake", we.Init{})
	w.Connect("hub", "p1", "sea_route", 10.0)
	w.Connect("hub", "l1", "shore_access", 1.0)

	hub := w.Entity("hub")
	ports := hub.Neighbors(we.Filter{Type: "Port"})
	lakes := hub.Neighbors(we.Filter{Type: "Lake"})

	if ports.Count() != 1 || ports.Entities()[0].ID() != "p1" {
		t.Errorf("type filter Port: got %d", ports.Count())
	}
	if lakes.Count() != 1 || lakes.Entities()[0].ID() != "l1" {
		t.Errorf("type filter Lake: got %d", lakes.Count())
	}
}

func TestGraph_ConnTypeFilter(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})
	w.Spawn("c", "Node", we.Init{})
	w.Connect("a", "b", "sea_route", 5.0)
	w.Connect("a", "c", "shore_access", 2.0)

	a := w.Entity("a")
	seaNeighbors := a.Neighbors(we.Filter{ConnType: "sea_route"})
	if seaNeighbors.Count() != 1 || seaNeighbors.Entities()[0].ID() != "b" {
		t.Errorf("ConnType filter: got %d, want 1 (b)", seaNeighbors.Count())
	}
}

func TestGraph_NearestByWeight(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("hub", "Node", we.Init{})
	w.Spawn("close", "Node", we.Init{})
	w.Spawn("far", "Node", we.Init{})
	w.Connect("hub", "close", "road", 1.0)
	w.Connect("hub", "far", "road", 100.0)

	hub := w.Entity("hub")
	nearest := hub.Neighbors().Nearest()
	if nearest == nil || nearest.ID() != "close" {
		t.Errorf("Nearest by weight: got %v, want close", nearest)
	}
}

func TestGraph_MutableConnectionsQueued(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})

	// Queue a connection add
	w.Entity("a").ConnectTo("b", "new_link", 3.0)
	w.ApplyPendingConnectChanges()

	neighbors := w.Entity("a").Neighbors()
	if neighbors.Count() != 1 || neighbors.Entities()[0].ID() != "b" {
		t.Errorf("mutable connect: got %d neighbors", neighbors.Count())
	}
}

func TestGraph_Snapshot(t *testing.T) {
	w := newTestWorld()
	w.Type("Node")
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})
	w.Connect("a", "b", "road", 1.0)

	snap := w.GraphSnapshot()

	// Modify — add another edge of a different type to a new node
	w.Spawn("c", "Node", we.Init{})
	w.Connect("a", "c", "shortcut", 0.5)
	if w.Entity("a").Neighbors().Count() != 2 {
		t.Error("expected 2 neighbors after adding shortcut to c")
	}

	// Restore — c connection should be gone
	w.RestoreGraph(snap)
	if w.Entity("a").Neighbors().Count() != 1 {
		t.Errorf("after restore: got %d connections, want 1", w.Entity("a").Neighbors().Count())
	}
}

func TestP_Accessors(t *testing.T) {
	p := we.P{"speed": 5.0, "name": "boat", "active": true}

	if got := p.Float("speed"); got != 5.0 {
		t.Errorf("Float: got %v", got)
	}
	if got := p.String("name"); got != "boat" {
		t.Errorf("String: got %v", got)
	}
	if got := p.Bool("active"); !got {
		t.Errorf("Bool: got %v", got)
	}
	if got := p.FloatOr("missing", 99.0); got != 99.0 {
		t.Errorf("FloatOr missing: got %v", got)
	}
}
