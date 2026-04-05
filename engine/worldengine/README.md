# worldengine

The core Terra Incognita simulation library. World authors define arbitrary worlds in Go; the engine runs them, dispatches AI agents, and persists everything to a SQLite run log.

```go
import we "github.com/shannonbay/terra-incognita/engine/worldengine"
```

## Concepts

| Concept | Description |
|---|---|
| **World** | Top-level container. Holds types, entities, connections, the tick loop, and the run log. |
| **TypeDef** | Schema + behaviour for a category of entity. Registers resource defaults, params, tick function, actions, and optional agent config. |
| **Entity** | The universal simulation object. Has resources (mutable state), params (immutable config), connections to neighbours, optional containment inside another entity, and optional agent backing. |
| **Resource** | Per-entity mutable value. Can be scalar (`float64`), `string`, `bool`, `Set`, `Queue`, `Map`, or `List`. |
| **Param** | Immutable per-entity value set at spawn time. Read by `e.Param("name")`. |
| **Action** | Named behaviour registered on a TypeDef. Invoked by entities during a tick. Local actions require the invoker to be colocated with the target; `RemoteAction` bypasses this. |
| **Connection** | Typed, weighted edge between entities. Bidirectional or directed. |
| **Containment** | Entities can be placed inside other entities (`World.Place`). |
| **Provider** | External agent process connected via HTTP `POST /decide`. Receives a perception snapshot each tick, returns a list of actions. |
| **RunLog** | SQLite database capturing every resource delta, action, spawn/destroy, and periodic full snapshots. Any past tick is reconstructable. |

---

## World lifecycle

```go
// 1. Create
w := we.New(we.Config{
    Name:     "fishing",
    MaxTicks: 365,
    DT:       1.0,       // one tick = one day
    Log: we.LogConfig{Enabled: true, Dir: "./runs", SnapshotInterval: 10},
})

// 2. Define types
boat := w.Type("Boat")
boat.Params(we.P{"capacity": 100.0})
boat.Resources(we.P{"cargo": 0.0, "fuel": 100.0})
boat.Tick(func(e *we.Entity, dt float64) {
    e.Set("fuel", e.Get("fuel") - 1.0*dt)
})

ground := w.Type("FishingGround")
ground.Resources(we.P{"fish": 500.0})
ground.Hidden("fish")   // agents can't directly observe this

// 3. Register actions
ground.Action("fish", func(target, invoker *we.Entity, p we.P) we.ActionResult {
    amount := p.FloatOr("amount", 10.0)
    if target.Get("fish") < amount {
        return we.Fail("not enough fish")
    }
    target.Set("fish", target.Get("fish")-amount)
    invoker.Set("cargo", invoker.Get("cargo")+amount)
    return we.OK()
})

// 4. Register agent provider
w.Provider("player", we.ProviderConfig{
    Endpoint:  "http://localhost:9090",
    TimeoutMs: 5000,
})

// 5. Configure agent type
boat.Agent(we.AgentConfig{
    Provider:   "player",
    Prompt:     "You are a fishing boat captain. Maximise your haul.",
    Perception: []string{
        "/self/resources",
        "/entities[type=FishingGround]/resources/fish_stock",
    },
})

// 6. Spawn entities
w.Spawn("boat1", "Boat", we.Init{})
w.Spawn("ground1", "FishingGround", we.Init{
    Params: we.P{"regen": 5.0},
})
w.Place("boat1", "ground1")

// 7. Run
w.Run()

// 8. Analyse
fmt.Println("final cargo:", w.Entity("boat1").Get("cargo"))
fmt.Println("run log:", w.RunLogPath())
```

---

## TypeDef API

Obtained via `w.Type("Name")`.

```go
t.Params(defaults P)                   // set default param values for this type
t.Resources(defaults P)                // set default resource values
t.Hidden("resource")                   // hide resource from external agent perception
t.Private("resource")                  // hide from all other entities (even non-agents)

t.Tick(func(e *Entity, dt float64))    // per-tick behaviour function

t.Action("name", handler)              // local action (invoker must be colocated with target)
t.RemoteAction("name", handler)        // action callable from anywhere

t.Agent(AgentConfig{...})              // back this type with an external agent provider
```

Action handler signature: `func(target, invoker *Entity, p P) ActionResult`

Return `we.OK()` or `we.Fail("reason")`.

**Colocation rule**: a regular `Action` on type T can only be invoked when the invoker is inside the target OR the target is inside the invoker. Use `RemoteAction` when the invoker and target are siblings (both inside the same room) or in different containers.

---

## Entity API

```go
// Scalar resources
e.Get("fuel")                   // float64, panics if absent or wrong type
e.GetOr("fuel", 0.0)            // float64 with default
e.Set("fuel", 80.0)             // set, buffers a delta for the log
e.Has("fuel")                   // bool

// Params (immutable)
e.Param("capacity")             // float64, panics if absent

// Complex resources
e.ListPush("log", item)         // append to List
e.ListPop("log")                // remove and return last item
e.QueuePush("msgs", msg)        // enqueue
e.QueuePop("msgs")              // dequeue
e.SetAdd("visited", "port1")    // add to Set
e.SetHas("visited", "port1")    // bool
e.MapSet("data", "key", val)    // set map entry
e.MapGet("data", "key")         // retrieve map entry (nil if absent)

// Spatial
e.Location()                    // containing entity, or nil
e.Contains()                    // EntitySet of entities inside this one
e.Neighbors(filters...)         // EntitySet of connected entities

// Identity
e.ID()                          // string
e.Type()                        // type name string

// Actions (called from tick functions or action handlers)
e.Act("fish", "ground1", we.P{"amount": 20.0})   // queue an action
e.Move("ground2")                                  // queue a movement
e.Spawn("boat2", "Boat", we.Init{})               // queue a spawn
e.Destroy()                                        // queue self-destruction
```

**Float literal trap**: all numeric resource and param values must be `float64`. Writing `"fuel": 0` (untyped int) will panic at spawn. Always write `"fuel": 0.0`.

---

## Resource types

| Type | Init value | API |
|---|---|---|
| Scalar float64 | `"fuel": 50.0` | `Get`, `Set` |
| Scalar string | `"name": "Alice"` | Read via `MapGet("name", "")` or type-assert |
| Set | `"visited": we.NewSet()` | `SetAdd`, `SetHas`, `SetRemove` |
| Queue | `"inbox": we.NewQueue()` | `QueuePush`, `QueuePop` |
| Map | `"data": we.NewMap()` | `MapSet`, `MapGet` |
| List | `"log": []any{}` | `ListPush`, `ListPop` |

---

## Actions in tick functions

Entities queue actions during tick functions; the engine dispatches them after all ticks complete (Phase 4):

```go
boat.Tick(func(e *we.Entity, dt float64) {
    if e.Get("cargo") < 50 {
        // fish at the ground this boat is inside
        e.Act("fish", "", we.P{"amount": 10.0})
    }
    if e.Get("fuel") < 20 {
        e.Move("port1")
    }
})
```

Pass `""` as target to use the default resolution (invoker's location, then self).

---

## Query language

Path-based expressions for inspecting world state from tick functions, scoring, MCP tools, and agent perception configs:

```
/entities[type=FishingGround]/resources/fish          // all fish values
/entities[type=Boat]/resources/cargo/@sum             // total cargo
/entities[type=Boat][cargo>50]/resources              // boats with cargo > 50
/self/neighbors[type=Node]/resources/pheromone/@max   // max pheromone among neighbours
/world/config/tick                                    // current tick number
```

**Predicates**: `=`, `>`, `<`, `>=`, `<=`

**Aggregators** (appended with `@`): `@sum`, `@mean`, `@min`, `@max`, `@count`

```go
// From a scoring function or tick function
w.QueryFloat("/entities[type=Boat]/resources/cargo/@sum")
w.QueryEntities("/entities[type=FishingGround][fish<100]")

// From an entity
e.Query("/self/neighbors[type=Port]/resources/docking_fee/@min")
```

---

## Scoring

```go
// Terminal — evaluated once at end of run
w.Score(func(w *we.World, agentID string) float64 {
    return w.Entity(agentID).Get("cargo")
})

// Continuous — sampled every tick, aggregated at end
w.ScoreContinuous(func(w *we.World, agentID string, tick int) float64 {
    return w.Entity(agentID).Get("wellbeing")
}, we.AggregateMean)

// Visibility — what the agent is told about scoring
w.ScoreVisibility(we.Hidden)              // agent sees nothing (default for evaluation worlds)
w.ScoreVisibility(we.Public)             // agent sees its current score
w.ScoreHint("Maximise total islander wellbeing") // agent sees a goal description only
```

Final score = terminal score + aggregated continuous score.

---

## Connections

```go
w.Connect("island1", "island2", "sea_route", 5.2)        // bidirectional
w.ConnectDirected("source", "sink", "flow", 1.0)         // one-way

// Custom description for agent perception
w.ConnectionDescription(func(c we.Connection) string {
    return fmt.Sprintf("%s (distance %.1f)", c.Type, c.Weight)
})

// Movement cost check (return false to block the move)
w.MovementCost(func(mover *we.Entity, conn we.Connection) bool {
    cost := conn.Weight
    if mover.Get("fuel") < cost {
        return false
    }
    mover.Set("fuel", mover.Get("fuel")-cost)
    return true
})
```

---

## Agent perception

Agents receive the result of their `Perception` query list each tick. Hidden resources are stripped before delivery. The harness calls `w.QueryAgentPerception(expr, agentID)` for each expression.

```go
boat.Agent(we.AgentConfig{
    Provider: "player",
    Prompt:   "You are a fishing boat.",
    Perception: []string{
        "/self/resources",                                    // own fuel, cargo
        "/entities[type=FishingGround]/resources",            // visible ground resources
        "/self/neighbors[type=Port]/resources/docking_fee",   // nearby port fees
        "/world/config/tick",                                 // current tick
    },
    TickFrequency: 1, // called every tick (default)
})
```

---

## Run log

Every run with `Log.Enabled: true` writes a SQLite database:

```go
// After the run
logPath := w.RunLogPath()

// Re-open for analysis
rl, err := we.OpenRunLog(logPath)
defer rl.Close()

// Reconstruct world state at tick 42
state, err := rl.StateAt(42)

// Query events
events, err := rl.Events(we.EventQuery{
    TickFrom: 0, TickTo: 50,
    EntityID: "boat1",
})
for _, ev := range events {
    fmt.Println(ev.EventType, ev.Field, ev.OldValue, "→", ev.NewValue)
}
```

Event types: `resource_delta`, `action`, `action_fail`, `spawn`, `destroy`, `move`, `score`.

---

## MCP server

Expose a running world to any MCP-compatible LLM:

```go
srv := we.NewMCPServer(world)
srv.ServeStdio(ctx)  // wire up in .mcp.json
```

Available MCP tools: `step`, `step_back`, `run`, `pause`, `resume`, `query`, `list_entities`, `set_resource`, `snapshot`, `restore`, `get_events`, `get_state_at_tick`, `load_log`, `unload_log`, `list_runs`, `run_tournament`.

---

## Tournament

Run multiple agents against multiple worlds and rank them:

```go
tr := we.NewTournament(we.TournamentConfig{
    Name:         "benchmark-q1",
    RunsPerWorld: 10,
    Aggregation:  we.AggregateMean,
})

tr.AddWorld("fishing", func() *we.World { return buildFishingWorld() })
tr.AddWorld("market",  func() *we.World { return buildMarketWorld()  })

tr.AddAgent("agent-a", we.ProviderConfig{Endpoint: "http://localhost:8080"})
tr.AddAgent("agent-b", we.ProviderConfig{Endpoint: "http://localhost:8081"})

results := tr.Run()
results.PrintLeaderboard()
```

World factories are called fresh per run, so every run is isolated.

---

## Common mistakes

| Mistake | Fix |
|---|---|
| `"fuel": 0` in `Resources` | Use `"fuel": 0.0` — must be `float64` |
| Regular `Action` between siblings | Use `RemoteAction` — siblings are not colocated |
| Mutating entity state outside `Set` | Always use `Set` so deltas are captured for the log |
| Calling `e.Act` after Phase 1 | Actions queued anywhere in a tick function are fine; they dispatch in Phase 4 |
| Trying to read a hidden resource from another entity | Hidden resources are stripped at perception build time; query from inside the owning entity's tick function instead |

---

## Tick loop (spec §10.2)

Each call to `w.Run()` executes up to `MaxTicks` iterations. Each tick:

1. **Tick functions** — all entity tick functions run in deterministic order
2. **Agent dispatch** — HTTP calls to all agent providers, in parallel
3. **Merge queues** — tick-function actions + agent actions combined
4. **Action dispatch** — all non-move actions execute
5. **Move transitions** — movement actions execute, applying `MovementCost`
6. **Continuous scoring** — per-tick score samples recorded
7. **Delta flush** — resource deltas written to run log
8. **Snapshot + lifecycle** — periodic snapshots written; spawns/destroys applied; graph changes applied
