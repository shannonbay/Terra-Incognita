package worldengine_test

import (
	"math"
	"testing"

	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

// ----------------------------------------------------------------------------
// Deterministic tick ordering
// ----------------------------------------------------------------------------

func TestTick_DeterministicOrder(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 1})

	// Three entities with IDs that sort lexicographically: "a" < "b" < "c"
	var order []string
	w.Type("Node").Tick(func(e *we.Entity, dt float64) {
		order = append(order, e.ID())
	})
	w.Spawn("c", "Node", we.Init{})
	w.Spawn("a", "Node", we.Init{})
	w.Spawn("b", "Node", we.Init{})

	w.Step(1)

	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("tick order: got %v, want [a b c]", order)
	}
}

// ----------------------------------------------------------------------------
// Action collection and dispatch
// ----------------------------------------------------------------------------

func TestTick_ActionDispatch(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 1})

	// FishingGround exposes a "fish" action
	ground := w.Type("FishingGround")
	ground.Resources(we.P{"fish_stock": 1000.0})
	ground.Action("fish", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
		skill := p.Float("skill")
		caught := math.Min(skill*0.3, target.Get("fish_stock"))
		target.Set("fish_stock", target.Get("fish_stock")-caught)
		invoker.Set("catch", invoker.Get("catch")+caught)
		return we.OK()
	})

	// Boat is inside the lake and fishes each tick
	boat := w.Type("Boat")
	boat.Resources(we.P{"catch": 0.0})
	boat.Tick(func(e *we.Entity, dt float64) {
		e.Act("fish", we.P{"skill": 10.0})
	})

	w.Spawn("lake1", "FishingGround", we.Init{})
	w.Spawn("boat1", "Boat", we.Init{Location: "lake1"})

	w.Step(1)

	boat1 := w.Entity("boat1")
	caught := boat1.Get("catch")
	if caught != 3.0 { // skill 10 * 0.3 = 3
		t.Errorf("catch after 1 tick: got %v, want 3.0", caught)
	}

	lake := w.Entity("lake1")
	if lake.Get("fish_stock") != 997.0 {
		t.Errorf("fish_stock after fishing: got %v, want 997.0", lake.Get("fish_stock"))
	}
}

// ----------------------------------------------------------------------------
// MaxActionsPerTick enforcement
// ----------------------------------------------------------------------------

func TestTick_MaxActionsPerTick(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 1, MaxActionsPerTick: 1})

	counter := 0
	ground := w.Type("Ground")
	ground.Resources(we.P{"x": 0.0})
	ground.Action("inc", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
		counter++
		target.Set("x", target.Get("x")+1)
		return we.OK()
	})

	// Actor queues 3 actions but limit is 1
	actor := w.Type("Actor")
	actor.Tick(func(e *we.Entity, dt float64) {
		e.Act("inc", we.P{})
		e.Act("inc", we.P{})
		e.Act("inc", we.P{})
	})

	w.Spawn("g", "Ground", we.Init{})
	w.Spawn("a", "Actor", we.Init{Location: "g"})

	w.Step(1)

	if counter != 1 {
		t.Errorf("MaxActionsPerTick=1: got %d actions executed, want 1", counter)
	}
}

// ----------------------------------------------------------------------------
// Built-in move (TransitionManager)
// ----------------------------------------------------------------------------

func TestTick_Move(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 1})

	w.Type("Lake")
	w.Type("Boat").Tick(func(e *we.Entity, dt float64) {
		// Move to lake2 on tick 0
		e.Act("move", we.P{"target": "lake2"})
	})

	w.Spawn("lake1", "Lake", we.Init{})
	w.Spawn("lake2", "Lake", we.Init{})
	w.Connect("lake1", "lake2", "water", 1.0)
	w.Spawn("boat1", "Boat", we.Init{Location: "lake1"})

	w.Step(1)

	boat := w.Entity("boat1")
	loc := boat.Location()
	if loc == nil || loc.ID() != "lake2" {
		t.Errorf("after move: location = %v, want lake2", loc)
	}
}

func TestTick_MoveUnreachableFails(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 1})

	w.Type("Lake")
	w.Type("Boat").Tick(func(e *we.Entity, dt float64) {
		// lake3 is not connected
		e.Act("move", we.P{"target": "lake3"})
	})

	w.Spawn("lake1", "Lake", we.Init{})
	w.Spawn("lake3", "Lake", we.Init{})
	// No connection between lake1 and lake3
	w.Spawn("boat1", "Boat", we.Init{Location: "lake1"})

	w.Step(1)

	boat := w.Entity("boat1")
	loc := boat.Location()
	if loc == nil || loc.ID() != "lake1" {
		t.Errorf("unreachable move: boat moved when it should not have; loc = %v", loc)
	}
}

func TestTick_MoveCostBlocks(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 1})

	// Movement costs more fuel than the boat has
	w.MovementCost(func(mover *we.Entity, conn we.Connection) bool {
		cost := conn.Weight * 10
		if mover.GetOr("fuel", 0) < cost {
			return false
		}
		mover.Set("fuel", mover.Get("fuel")-cost)
		return true
	})

	w.Type("Lake")
	boatT := w.Type("Boat")
	boatT.Resources(we.P{"fuel": 5.0})
	boatT.Tick(func(e *we.Entity, dt float64) {
		e.Act("move", we.P{"target": "lake2"})
	})

	w.Spawn("lake1", "Lake", we.Init{})
	w.Spawn("lake2", "Lake", we.Init{})
	w.Connect("lake1", "lake2", "sea", 2.0) // cost = 2*10 = 20, boat has only 5
	w.Spawn("boat1", "Boat", we.Init{Location: "lake1", Resources: we.P{"fuel": 5.0}})

	w.Step(1)

	if w.Entity("boat1").Location().ID() != "lake1" {
		t.Error("move with insufficient fuel should fail")
	}
}

// ----------------------------------------------------------------------------
// Entity lifecycle — spawn during simulation
// ----------------------------------------------------------------------------

func TestTick_SpawnMidSim(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 2})

	spawned := false
	w.Type("Factory").Tick(func(e *we.Entity, dt float64) {
		if !spawned {
			e.Spawn("product1", "Product", we.Init{})
			spawned = true
		}
	})
	w.Type("Product").Resources(we.P{"value": 1.0})

	w.Spawn("factory1", "Factory", we.Init{})

	w.Step(1) // spawn queued during tick 0, applied at end

	// Product should exist after tick 0 completes
	if w.Entity("product1") == nil {
		t.Error("spawned entity not present after tick 0")
	}
}

// ----------------------------------------------------------------------------
// Entity lifecycle — destroy during simulation
// ----------------------------------------------------------------------------

func TestTick_DestroyMidSim(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 2})

	ephT := w.Type("Ephemeral")
	ephT.Resources(we.P{"hp": 1.0})
	ephT.Tick(func(e *we.Entity, dt float64) {
		e.Set("hp", e.Get("hp")-1)
		if e.Get("hp") <= 0 {
			e.DestroySelf()
		}
	})

	w.Spawn("e1", "Ephemeral", we.Init{})

	w.Step(1) // tick 0: hp goes to 0, DestroySelf queued, applied at end

	if w.Entity("e1") != nil {
		t.Error("destroyed entity still present after tick 0")
	}
}

// ----------------------------------------------------------------------------
// Multiple ticks — state accumulates correctly
// ----------------------------------------------------------------------------

func TestTick_MultiTick(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 10})

	// Simple counter: x += 1 each tick
	cntT := w.Type("Counter")
	cntT.Resources(we.P{"x": 0.0})
	cntT.Tick(func(e *we.Entity, dt float64) {
		e.Set("x", e.Get("x")+dt)
	})
	w.Spawn("c1", "Counter", we.Init{})

	w.Step(5)

	if got := w.Entity("c1").Get("x"); got != 5.0 {
		t.Errorf("after 5 ticks: x = %v, want 5.0", got)
	}
}

// ----------------------------------------------------------------------------
// CurrentTick advances correctly
// ----------------------------------------------------------------------------

func TestTick_CurrentTick(t *testing.T) {
	w := we.New(we.Config{DT: 1.0, MaxTicks: 10})
	w.Type("Empty")
	w.Spawn("e", "Empty", we.Init{})

	if w.CurrentTick() != 0 {
		t.Errorf("initial tick: got %d, want 0", w.CurrentTick())
	}
	w.Step(3)
	if w.CurrentTick() != 3 {
		t.Errorf("after Step(3): got %d, want 3", w.CurrentTick())
	}
}
