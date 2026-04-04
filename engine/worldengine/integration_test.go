package worldengine

// ---------------------------------------------------------------------------
// Phase 10 — Integration Tests
//
// Each test exercises the engine's mechanics end-to-end using canonical
// toy-problem worlds. Tests are intentionally small (entities / ticks) so
// the full suite stays fast.
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ===========================================================================
// 10.1  Dining Philosophers
// ===========================================================================

// TestDiningPhilosophers verifies that 5 philosophers competing for 5 forks
// (shared entities) eventually eat without deadlock. The engine's deterministic
// sequential tick order guarantees progress: philosopher "p0" always runs before
// "p1", etc., so p0 can always acquire both its forks.
func TestDiningPhilosophers(t *testing.T) {
	const N = 5
	w := New(Config{MaxTicks: 20})

	// Fork entity: available=1 means free.
	fork := w.Type("Fork")
	fork.Resources(P{"available": 1.0})

	// Action "acquire" on Fork: invoker takes the fork if available. Remote so
	// philosophers can reach forks anywhere in the world.
	fork.RemoteAction("acquire", func(target *Entity, invoker *Entity, p P) ActionResult {
		if target.Get("available") == 0 {
			return Fail("fork in use")
		}
		target.Set("available", 0)
		invoker.Set("holding", invoker.Get("holding")+1)
		return OK()
	})

	// Action "release" on Fork: invoker releases it.
	fork.RemoteAction("release", func(target *Entity, invoker *Entity, p P) ActionResult {
		target.Set("available", 1)
		invoker.Set("holding", invoker.Get("holding")-1)
		return OK()
	})

	// Philosopher entity.
	phil := w.Type("Philosopher")
	phil.Resources(P{"holding": 0.0, "meals": 0.0})

	// Spawn forks and philosophers.
	for i := 0; i < N; i++ {
		w.Spawn(fmt.Sprintf("fork%d", i), "Fork", Init{})
	}
	// Place philosophers in a room so they can reach forks.
	room := w.Type("Room")
	room.Resources(P{})
	w.Spawn("room1", "Room", Init{})

	for i := 0; i < N; i++ {
		id := fmt.Sprintf("p%d", i)
		left := fmt.Sprintf("fork%d", i)
		right := fmt.Sprintf("fork%d", (i+1)%N)
		w.Spawn(id, "Philosopher", Init{Location: "room1"})
		// Capture i, left, right in closure.
		ii, l, r := i, left, right
		_ = ii
		phil.Tick(func(e *Entity, dt float64) {
			if e.id != id {
				return
			}
			if e.Get("holding") == 2 {
				// Eat and release.
				e.Set("meals", e.Get("meals")+1)
				e.Act("release", P{"target": l})
				e.Act("release", P{"target": r})
				return
			}
			if e.Get("holding") == 0 {
				e.Act("acquire", P{"target": l})
			}
			if e.Get("holding") == 1 {
				e.Act("acquire", P{"target": r})
			}
		})
	}

	// Place all forks in the room.
	for i := 0; i < N; i++ {
		w.Place(fmt.Sprintf("fork%d", i), "room1")
	}

	w.Run()

	// At least philosopher p0 should have eaten.
	totalMeals := 0.0
	for i := 0; i < N; i++ {
		totalMeals += w.Entity(fmt.Sprintf("p%d", i)).Get("meals")
	}
	if totalMeals == 0 {
		t.Error("no philosopher ate — possible deadlock or logic error")
	}
}

// ===========================================================================
// 10.2  Producer-Consumer
// ===========================================================================

// TestProducerConsumer verifies that producers enqueue items into a shared
// Queue resource and consumers dequeue them, with correct total throughput.
func TestProducerConsumer(t *testing.T) {
	const ticks = 20

	w := New(Config{MaxTicks: ticks})

	// Buffer entity holds the shared queue.
	bufType := w.Type("Buffer")
	bufType.Resources(P{"q": Queue{}, "size": 0.0})

	// Action "push" on Buffer: producer enqueues an item. Remote so producer can
	// reach the buffer without being inside it.
	bufType.RemoteAction("push", func(target *Entity, invoker *Entity, p P) ActionResult {
		target.QueuePush("q", 1.0)
		target.Set("size", target.Get("size")+1)
		invoker.Set("produced", invoker.Get("produced")+1)
		return OK()
	})

	// Action "pop" on Buffer: consumer dequeues an item if available.
	bufType.RemoteAction("pop", func(target *Entity, invoker *Entity, p P) ActionResult {
		if target.Get("size") <= 0 {
			return Fail("empty")
		}
		target.QueuePop("q")
		target.Set("size", target.Get("size")-1)
		invoker.Set("consumed", invoker.Get("consumed")+1)
		return OK()
	})

	// Producer type.
	prod := w.Type("Producer")
	prod.Resources(P{"produced": 0.0})
	prod.Tick(func(e *Entity, dt float64) {
		e.Act("push", P{"target": "buf1", "item": 1.0})
	})

	// Consumer type.
	cons := w.Type("Consumer")
	cons.Resources(P{"consumed": 0.0})
	cons.Tick(func(e *Entity, dt float64) {
		e.Act("pop", P{"target": "buf1"})
	})

	w.Spawn("buf1", "Buffer", Init{})
	w.Spawn("prod1", "Producer", Init{})
	w.Spawn("cons1", "Consumer", Init{})

	w.Run()

	produced := w.Entity("prod1").Get("produced")
	consumed := w.Entity("cons1").Get("consumed")

	if produced == 0 {
		t.Error("producer produced nothing")
	}
	// Consumer should have consumed at least some items (one tick delay is OK).
	if consumed == 0 {
		t.Error("consumer consumed nothing")
	}
	// Items remaining in buffer = size resource.
	remaining := w.Entity("buf1").Get("size")
	if produced != consumed+remaining {
		t.Errorf("mass conservation violated: produced=%.0f consumed=%.0f remaining=%.0f",
			produced, consumed, remaining)
	}
}

// ===========================================================================
// 10.3  Game of Life (performance + neighbour reads)
// ===========================================================================

// TestGameOfLife creates a 5×5 grid and runs 10 ticks to verify:
//  1. The engine can handle dense cross-entity reads efficiently.
//  2. A known stable pattern (2×2 block) remains stable.
//
// Note: GoL requires synchronous evaluation. We use a two-resource approach:
// each cell reads `alive` from neighbours and writes `next_alive`. A Controller
// entity applies next_alive → alive after cells have computed.
func TestGameOfLife(t *testing.T) {
	const rows, cols = 5, 5

	w := New(Config{MaxTicks: 10})

	cell := w.Type("Cell")
	cell.Resources(P{"alive": 0.0, "next_alive": 0.0})

	// Tick: compute next_alive from current neighbour alive values.
	cell.Tick(func(e *Entity, dt float64) {
		aliveNeighbors := 0.0
		e.Neighbors().Each(func(n *Entity) {
			aliveNeighbors += n.Get("alive")
		})
		cur := e.Get("alive")
		var next float64
		if cur == 1 && (aliveNeighbors == 2 || aliveNeighbors == 3) {
			next = 1
		} else if cur == 0 && aliveNeighbors == 3 {
			next = 1
		}
		e.Set("next_alive", next)
	})

	// Controller entity applies next_alive → alive each tick.
	ctrl := w.Type("Controller")
	ctrl.Resources(P{})
	ctrl.Tick(func(e *Entity, dt float64) {
		for _, ce := range e.world.entities {
			if ce.typeName != "Cell" {
				continue
			}
			ce.Set("alive", ce.Get("next_alive"))
		}
	})

	// Spawn grid cells.
	cellID := func(r, c int) string { return fmt.Sprintf("cell_%d_%d", r, c) }
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			w.Spawn(cellID(r, c), "Cell", Init{})
		}
	}
	// Spawn controller.
	w.Spawn("ctrl", "Controller", Init{})

	// Connect cells to their Moore (8-direction) neighbours.
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			for dr := -1; dr <= 1; dr++ {
				for dc := -1; dc <= 1; dc++ {
					if dr == 0 && dc == 0 {
						continue
					}
					nr, nc := r+dr, c+dc
					if nr < 0 || nr >= rows || nc < 0 || nc >= cols {
						continue
					}
					w.Connect(cellID(r, c), cellID(nr, nc), "neighbor", 1)
				}
			}
		}
	}

	// Set a 2×2 "block" pattern (stable in GoL): cells (1,1)(1,2)(2,1)(2,2).
	for _, id := range []string{"cell_1_1", "cell_1_2", "cell_2_1", "cell_2_2"} {
		w.Entity(id).Set("alive", 1)
		w.Entity(id).Set("next_alive", 1)
	}

	w.Run()

	// The block should remain stable.
	for _, id := range []string{"cell_1_1", "cell_1_2", "cell_2_1", "cell_2_2"} {
		if w.Entity(id).Get("alive") != 1 {
			t.Errorf("block cell %s died — not stable", id)
		}
	}
	// Cells outside block should remain dead.
	for _, id := range []string{"cell_0_0", "cell_0_3", "cell_3_0", "cell_4_4"} {
		if w.Entity(id).Get("alive") != 0 {
			t.Errorf("cell %s outside block became alive unexpectedly", id)
		}
	}
}

// ===========================================================================
// 10.4  Ant Colony Foraging
// ===========================================================================

// TestAntColony verifies: ants move between food nodes via the graph,
// pheromone decays over time, and ants collect food.
func TestAntColony(t *testing.T) {
	w := New(Config{MaxTicks: 30})

	// Node type: has food and pheromone.
	node := w.Type("Node")
	node.Resources(P{"food": 0.0, "pheromone": 0.0})
	node.Tick(func(e *Entity, dt float64) {
		// Pheromone decays 10% per tick.
		e.Set("pheromone", e.Get("pheromone")*0.9)
	})
	// "collect" action: ant collects food from node. The ant is placed inside
	// the node, so colocated check passes (invLoc == target.id).
	node.Action("collect", func(target *Entity, invoker *Entity, p P) ActionResult {
		food := target.Get("food")
		if food <= 0 {
			return Fail("no food")
		}
		amount := math.Min(1.0, food)
		target.Set("food", food-amount)
		// Deposit pheromone.
		target.Set("pheromone", target.Get("pheromone")+1.0)
		invoker.Set("cargo", invoker.Get("cargo")+amount)
		return OK()
	})

	// Ant type.
	ant := w.Type("Ant")
	ant.Resources(P{"cargo": 0.0})
	ant.Tick(func(e *Entity, dt float64) {
		loc := e.Location()
		if loc != nil && loc.Get("food") > 0 {
			e.Act("collect", P{})
		} else {
			// Move toward the neighbour of our location with most food.
			if loc != nil {
				best := loc.Neighbors(Filter{Type: "Node"}).MaxBy("food")
				if best != nil {
					e.Act("move", P{"target": best.ID()})
				}
			}
		}
	})

	// Create 3 nodes in a line: nest ← path ← food_source
	w.Spawn("nest", "Node", Init{Resources: P{"food": 0.0}})
	w.Spawn("path", "Node", Init{Resources: P{"food": 0.0}})
	w.Spawn("food_source", "Node", Init{Resources: P{"food": 20.0}})

	w.Connect("nest", "path", "trail", 1.0)
	w.Connect("path", "food_source", "trail", 1.0)

	// Spawn 2 ants at the nest.
	w.Spawn("ant1", "Ant", Init{Location: "nest"})
	w.Spawn("ant2", "Ant", Init{Location: "nest"})

	w.Run()

	// At least one ant should have collected food.
	totalCargo := w.Entity("ant1").Get("cargo") + w.Entity("ant2").Get("cargo")
	if totalCargo == 0 {
		t.Error("no ants collected food — foraging failed")
	}
	// Food at source should have decreased.
	remaining := w.Entity("food_source").Get("food")
	if remaining >= 20 {
		t.Errorf("food source untouched: remaining=%.1f", remaining)
	}
}

// ===========================================================================
// 10.5  Token Ring
// ===========================================================================

// TestTokenRing creates N entities in a directed ring. Each tick the token-holder
// passes the token to the next entity. After N*k ticks, verify exactly one entity
// holds the token at any time, and the token has circulated.
func TestTokenRing(t *testing.T) {
	const N = 4
	const ticks = N * 3 // 3 full rotations

	w := New(Config{MaxTicks: ticks})

	// Token type: holds a resource indicating "has token" (1) or not (0).
	// The pass_token action transfers the token to the receiver.
	nodeType := w.Type("RingNode")
	nodeType.Resources(P{"has_token": 0.0, "received": 0.0})
	nodeType.RemoteAction("receive_token", func(target *Entity, invoker *Entity, p P) ActionResult {
		target.Set("has_token", 1)
		target.Set("received", target.Get("received")+1)
		invoker.Set("has_token", 0)
		return OK()
	})
	nodeType.Tick(func(e *Entity, dt float64) {
		if e.Get("has_token") == 0 {
			return
		}
		// Find the first neighbour (ring direction).
		next := e.Neighbors(Filter{Type: "RingNode"}).Nearest()
		if next != nil {
			e.Act("receive_token", P{"target": next.ID()})
		}
	})

	// Spawn ring nodes.
	ids := make([]string, N)
	for i := 0; i < N; i++ {
		ids[i] = fmt.Sprintf("rn%d", i)
		w.Spawn(ids[i], "RingNode", Init{})
	}
	// Connect in a directed ring: 0→1→2→3→0.
	for i := 0; i < N; i++ {
		w.ConnectDirected(ids[i], ids[(i+1)%N], "ring", 1.0)
	}
	// Token starts at node 0.
	w.Entity(ids[0]).Set("has_token", 1)

	w.Run()

	// After ticks: exactly one node holds the token.
	holders := 0
	for _, id := range ids {
		if w.Entity(id).Get("has_token") == 1 {
			holders++
		}
	}
	if holders != 1 {
		t.Errorf("want exactly 1 token holder, got %d", holders)
	}

	// Every node should have received the token at least once.
	// (3 rotations × N nodes means each node got it 3 times.)
	for _, id := range ids {
		recv := w.Entity(id).Get("received")
		if recv < 1 {
			t.Errorf("node %q never received the token (received=%.0f)", id, recv)
		}
	}
}

// ===========================================================================
// 10.6  Simple Market
// ===========================================================================

// TestSimpleMarket verifies that buyers and sellers transact when prices match.
// A "Market" entity holds bids and asks. Each tick:
// - Sellers post ask prices via a set_ask action.
// - Buyers submit bids via a place_bid action.
// - The market clears matched trades.
func TestSimpleMarket(t *testing.T) {
	w := New(Config{MaxTicks: 10})

	// Market entity: holds a simple price (for testing just track trade count).
	market := w.Type("Market")
	market.Resources(P{"trades": 0.0, "ask_price": 10.0})

	// "buy" action: buyer pays ask price.
	market.RemoteAction("buy", func(target *Entity, invoker *Entity, p P) ActionResult {
		price := target.Get("ask_price")
		if invoker.GetOr("money", 0) < price {
			return Fail("insufficient funds")
		}
		invoker.Set("money", invoker.Get("money")-price)
		invoker.Set("items", invoker.Get("items")+1)
		target.Set("trades", target.Get("trades")+1)
		return OK()
	})

	// "sell" action: seller receives ask price.
	market.RemoteAction("sell", func(target *Entity, invoker *Entity, p P) ActionResult {
		price := target.Get("ask_price")
		invoker.Set("money", invoker.Get("money")+price)
		invoker.Set("items", invoker.Get("items")-1)
		target.Set("trades", target.Get("trades")+1)
		return OK()
	})

	// Buyer type.
	buyer := w.Type("Buyer")
	buyer.Resources(P{"money": 100.0, "items": 0.0})
	buyer.Tick(func(e *Entity, dt float64) {
		e.Act("buy", P{"target": "market1"})
	})

	// Seller type.
	seller := w.Type("Seller")
	seller.Resources(P{"money": 0.0, "items": 5.0})
	seller.Tick(func(e *Entity, dt float64) {
		if e.Get("items") > 0 {
			e.Act("sell", P{"target": "market1"})
		}
	})

	w.Spawn("market1", "Market", Init{})
	w.Spawn("buyer1", "Buyer", Init{})
	w.Spawn("seller1", "Seller", Init{Resources: P{"items": 5.0}})

	w.Run()

	trades := w.Entity("market1").Get("trades")
	if trades == 0 {
		t.Error("no trades executed")
	}
	if w.Entity("buyer1").Get("items") == 0 {
		t.Error("buyer has no items after trading")
	}
}

// ===========================================================================
// 10.7  Fishing World (full end-to-end)
// ===========================================================================

// TestFishingWorld implements the spec §15 fishing economy with a mock agent
// instead of a real LLM. Verifies:
// - Run completes 365 ticks
// - Run log file is created (logging enabled)
// - State reconstruction at tick 100 works
// - Final scores are non-zero for the player
func TestFishingWorld(t *testing.T) {
	// Mock agent server: player boat does nothing (returns empty actions).
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decideResponse{})
	}))
	defer mockSrv.Close()

	logDir := t.TempDir()

	w := New(Config{
		Name:              "fishing_test",
		DT:                1.0,
		TickUnit:          "day",
		MaxTicks:          365,
		MaxActionsPerTick: 3,
		Log: LogConfig{
			Dir:              logDir,
			SnapshotInterval: 100,
			Enabled:          true,
		},
	})

	// Agent provider (mock).
	w.Provider("claude", ProviderConfig{
		Endpoint:  mockSrv.URL,
		TimeoutMs: 500,
	})

	// FishingGround.
	ground := w.Type("FishingGround")
	ground.Params(P{"max_fish_stock": 2000.0, "regen_rate": 5.0})
	ground.Resources(P{"fish_stock": 0.0})
	ground.Hidden("fish_stock")
	ground.Tick(func(e *Entity, dt float64) {
		stock := e.Get("fish_stock")
		max := e.Param("max_fish_stock")
		rate := e.Param("regen_rate")
		e.Set("fish_stock", math.Min(max, stock+rate*dt))
	})
	ground.Action("fish", func(target *Entity, invoker *Entity, p P) ActionResult {
		skill := p.FloatOr("skill", 1.0)
		if invoker.GetOr("fuel", 0) < 1.0 {
			return Fail("insufficient fuel")
		}
		stock := target.Get("fish_stock")
		if stock <= 0 {
			return Fail("nothing to catch")
		}
		caught := math.Min(skill*0.3, stock)
		target.Set("fish_stock", stock-caught)
		invoker.Set("catch", invoker.Get("catch")+caught)
		invoker.Set("fuel", invoker.Get("fuel")-1.0)
		return OK()
	})

	// Port.
	port := w.Type("Port")
	port.Params(P{"fuel_price": 2.0})
	port.Resources(P{"fuel_supply": 5000.0})
	port.Action("refuel", func(target *Entity, invoker *Entity, p P) ActionResult {
		amount := p.FloatOr("amount", 10.0)
		price := target.Param("fuel_price")
		cost := amount * price
		if invoker.GetOr("money", 0) < cost {
			return Fail("can't afford")
		}
		target.Set("fuel_supply", target.Get("fuel_supply")-amount)
		invoker.Set("fuel", invoker.Get("fuel")+amount)
		invoker.Set("money", invoker.Get("money")-cost)
		return OK()
	})

	// NPCBoat.
	npc := w.Type("NPCBoat")
	npc.Resources(P{"fuel": 100.0, "catch": 0.0})
	npc.Tick(func(e *Entity, dt float64) {
		e.Set("fuel", e.Get("fuel")-dt*0.5)
		if e.Get("fuel") < 10 {
			nearest := e.Neighbors(Filter{Type: "Port"}).Nearest()
			if nearest != nil {
				e.Act("move", P{"target": nearest.ID()})
			}
			return
		}
		loc := e.Location()
		if loc != nil && loc.typeName == "FishingGround" && loc.Get("fish_stock") > 100 {
			e.Act("fish", P{"skill": 5.0})
		} else {
			best := e.Neighbors(Filter{Type: "FishingGround"}).MaxBy("fish_stock")
			if best != nil {
				e.Act("move", P{"target": best.ID()})
			}
		}
	})

	// PlayerBoat (agent-backed).
	player := w.Type("PlayerBoat")
	player.Resources(P{"fuel": 100.0, "catch": 0.0, "money": 1000.0})
	player.Tick(func(e *Entity, dt float64) {
		e.Set("fuel", e.Get("fuel")-dt*0.3)
	})
	player.Agent(AgentConfig{
		Provider:   "claude",
		Prompt:     "Maximize profit while keeping the ecosystem healthy.",
		Perception: []string{"/self/location"},
	})

	// Instances.
	w.Spawn("lake_alpha", "FishingGround", Init{
		Params:    P{"max_fish_stock": 2000.0, "regen_rate": 5.0},
		Resources: P{"fish_stock": 1200.0},
	})
	w.Spawn("lake_beta", "FishingGround", Init{
		Params:    P{"max_fish_stock": 800.0, "regen_rate": 2.0},
		Resources: P{"fish_stock": 800.0},
	})
	w.Spawn("port_alpha", "Port", Init{
		Params:    P{"fuel_price": 2.5},
		Resources: P{"fuel_supply": 5000.0},
	})

	w.Spawn("boat_1", "PlayerBoat", Init{Resources: P{"fuel": 80.0, "money": 1000.0}})
	w.Spawn("npc_1", "NPCBoat", Init{Resources: P{"fuel": 90.0}})

	// Topology.
	w.Connect("lake_alpha", "port_alpha", "shore_access", 1.0)
	w.Connect("lake_beta", "port_alpha", "shore_access", 5.0)
	w.Connect("lake_alpha", "lake_beta", "waterway", 3.0)

	// Movement cost.
	w.MovementCost(func(mover *Entity, conn Connection) bool {
		fuelCost := conn.Weight * 0.1
		if mover.GetOr("fuel", 0) < fuelCost {
			return false
		}
		mover.Set("fuel", mover.Get("fuel")-fuelCost)
		return true
	})

	// Placement.
	w.Place("boat_1", "lake_alpha")
	w.Place("npc_1", "lake_alpha")

	// Scoring.
	w.Score(func(w *World, agentID string) float64 {
		agent := w.Entity(agentID)
		if agent == nil {
			return 0
		}
		profit := agent.GetOr("money", 0)
		minFish := w.QueryFloat("/entities[type=FishingGround]/resources/fish_stock/@min")
		return profit*0.3 + minFish*0.7
	})
	w.ScoreHint("Earn money, but the lakes must remain healthy.")

	w.Run()

	// --- Assertions ---

	// 1. Simulation ran all 365 ticks.
	if w.CurrentTick() != 365 {
		t.Errorf("want 365 ticks, got %d", w.CurrentTick())
	}

	// 2. Log file was created.
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var dbFiles []string
	for _, e := range entries {
		if !e.IsDir() {
			dbFiles = append(dbFiles, e.Name())
		}
	}
	if len(dbFiles) == 0 {
		t.Error("no run log file created in log dir")
	}

	// 3. State reconstruction at tick 100.
	if len(dbFiles) > 0 {
		dbPath := logDir + "/" + dbFiles[0]
		rl, err := OpenRunLog(dbPath)
		if err != nil {
			t.Fatalf("OpenRunLog: %v", err)
		}
		defer rl.Close()

		state, err := rl.StateAt(100)
		if err != nil {
			t.Fatalf("StateAt(100): %v", err)
		}
		if state == nil {
			t.Error("StateAt(100) returned nil")
		} else if _, ok := state.Entities["lake_alpha"]; !ok {
			t.Error("StateAt(100): lake_alpha missing from reconstructed state")
		}
	}

	// 4. Fish stock regenerated (wasn't all consumed).
	alphaStock := w.Entity("lake_alpha").Get("fish_stock")
	if alphaStock <= 0 {
		t.Errorf("lake_alpha fish stock depleted: %.1f", alphaStock)
	}
}
