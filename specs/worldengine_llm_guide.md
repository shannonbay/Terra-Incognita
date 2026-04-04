# World Engine API Guide — for LLM world authors

This guide is written for an LLM generating a new simulation world from scratch.
It covers the complete authoring API with annotated examples.
Skip the spec; read this first.

**Import path**

```go
import we "github.com/shannonbay/terra-incognita/engine/worldengine"
```

---

## 1. The pattern every world follows

```go
func main() {
    // 1. Create the world
    w := we.New(we.Config{
        Name:     "my-world",
        DT:       1.0,       // one tick = one "day" (or whatever unit you choose)
        TickUnit: "day",
        MaxTicks: 365,
        Log: we.LogConfig{
            Enabled:          true,
            Dir:              "./runs",
            SnapshotInterval: 100,
        },
    })

    // 2. Register types (schema + behaviour)
    tree := w.Type("Tree")
    tree.Params(we.P{"growth_rate": 1.0})   // immutable per-entity config
    tree.Resources(we.P{"height": 0.0})     // mutable state; always float64
    tree.Tick(func(e *we.Entity, dt float64) {
        e.Set("height", e.Get("height") + e.Param("growth_rate")*dt)
    })

    // 3. Spawn instances
    w.Spawn("oak1", "Tree", we.Init{Params: we.P{"growth_rate": 1.2}})
    w.Spawn("pine1", "Tree", we.Init{Resources: we.P{"height": 5.0}})

    // 4. Run
    w.Run()
}
```

That's the complete skeleton. Everything else is adding types, spawns, connections,
actions, and agents onto this frame.

---

## 2. Resources — the five kinds

Resources are the mutable state of an entity. All are declared in `TypeDef.Resources()`.
The default value in `Resources()` sets the type; the actual value stored depends on
what you pass.

| Kind | Declared as | Read | Write | Notes |
|---|---|---|---|---|
| **Scalar** | `"fuel": 0.0` | `e.Get("fuel")` | `e.Set("fuel", v)` | Always `float64`. Use `0.0` not `0`. |
| **Set** | `"tags": we.Set{}` | `e.SetHas("tags","x")` | `e.SetAdd("tags","x")` | Unique strings only. |
| **Queue** | `"inbox": we.Queue{}` | `e.QueuePop("inbox")` | `e.QueuePush("inbox", v)` | FIFO; `Pop` returns `any`, nil when empty. |
| **Map** | `"prices": map[string]any{}` | `e.MapGet("prices","fish")` | `e.MapSet("prices","fish",10.0)` | String-keyed, any value. |
| **List** | `"log": []any{}` | `e.ListPop("log")` | `e.ListPush("log", v)` | Ordered slice; append/pop from tail. |

> **Common mistake**: writing `"fuel": 0` (int literal) in a `we.P{}` map causes a
> panic at runtime. Always use `0.0`, `1.0`, etc.

**Params** follow the same declaration syntax but are read-only during the simulation:

```go
boat.Params(we.P{"capacity": 100.0, "speed": 2.0})
// in tick: e.Param("capacity")  — returns float64
```

---

## 3. Actions — and the colocated rule

Actions are named behaviours defined on a type and invoked by any entity.

### `Action` — for entities that contain or are contained by the invoker

```go
port := w.Type("Port")
port.Action("unload", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
    cargo := invoker.Get("cargo")
    if cargo <= 0 {
        return we.Fail("no cargo")
    }
    invoker.Set("cargo", 0)
    target.Set("gold", target.Get("gold")+cargo)
    return we.OK()
})

// A boat inside the port can call this:
// invoker is the boat, target is the port
boat.Tick(func(e *we.Entity, dt float64) {
    e.Act("unload", we.P{})  // targets the entity that contains the boat
})
```

**The colocated rule**: `Action` only executes if the invoker is *inside* the target
(`invoker.Location() == target`) or the target is *inside* the invoker. Sibling
entities (two boats both inside the same port) cannot target each other with `Action`.

### `RemoteAction` — for sibling entities or any cross-entity interaction

```go
fork := w.Type("Fork")
fork.RemoteAction("acquire", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
    if target.Get("held") > 0 {
        return we.Fail("already held")
    }
    target.Set("held", 1)
    return we.OK()
})

// A philosopher (sibling of the fork, both inside "table") can call:
philosopher.Tick(func(e *we.Entity, dt float64) {
    forkID := "fork_left"
    e.Act("acquire@"+forkID, we.P{})  // or use the target param in P
})
```

> **Rule of thumb**: if two entities are siblings (both inside the same container),
> use `RemoteAction`. When in doubt, use `RemoteAction` — it works everywhere.

### Invoking actions from a tick function

```go
// Target is implicit (the entity that contains the invoker, or a RemoteAction target):
e.Act("fish", we.P{"amount": 10.0})

// To target a specific entity by ID (RemoteAction):
e.Act("acquire@fork_left", we.P{})
```

Actions are **queued** during tick execution. They run after all tick functions
have completed. An entity cannot read the result of an action it invoked in the
same tick.

---

## 4. Containment and connections

```go
// Place entity inside a container (also settable at spawn time via Init.Location)
w.Place("boat1", "port_alpha")

// At spawn time:
w.Spawn("boat1", "Boat", we.Init{Location: "port_alpha"})

// Read location from inside a tick function:
loc := e.Location()          // *Entity, nil if top-level
locID := e.Location().ID()

// Typed weighted edges (undirected):
w.Connect("node_a", "node_b", "road", 1.0)

// Directed edge:
w.ConnectDirected("src", "dst", "pipe", 1.0)

// From inside a tick function (deferred until end of tick):
e.ConnectTo("other_id", "road", 1.0)
e.Disconnect("other_id", "road")

// Traversal:
neighbours := e.Neighbors()                          // all connections
byType     := e.Neighbors(we.Filter{ConnType: "road"})
byEntity   := e.Neighbors(we.Filter{Type: "City"})
best       := e.Neighbors(we.Filter{Type: "Node"}).MaxBy("food")

// What's inside an entity:
contents := e.Contains()                             // EntitySet
```

---

## 5. EntitySet — fluent results

`Neighbors()`, `Contains()`, and query results return an `EntitySet`.

```go
set := e.Neighbors(we.Filter{Type: "Node"})

set.Count()                   // int
set.Sum("cargo")              // float64 — sum of resource across all
set.Avg("health")             // float64
set.MaxBy("food")             // *Entity with highest "food", or nil
set.MinBy("distance")         // *Entity with lowest
set.Nearest()                 // *Entity with lowest connection weight
set.Filter(we.Filter{...})    // EntitySet — further narrow
set.Each(func(e *we.Entity) { ... })
set.Entities()                // []*Entity — raw slice
```

---

## 6. Agents (external LLM/script providers)

An entity type backed by an external agent receives a perception snapshot and
returns actions via HTTP every tick.

```go
// Register the agent type
boat := w.Type("Boat")
boat.Resources(we.P{"cargo": 0.0, "fuel": 100.0})
boat.Agent(we.AgentConfig{
    Provider:      "claude",
    Prompt:        "You are a fishing boat captain. Maximise cargo unloaded.",
    Perception:    []string{
        "/self/resources",
        "/self/location/resources",
        "/self/neighbors[type=FishingGround]/resources/fish_stock",
    },
    TickFrequency: 1,   // consult agent every tick; use 5 for every 5 ticks
})

// Register where "claude" connects
w.Provider("claude", we.ProviderConfig{
    Endpoint:  "http://localhost:8080",
    TimeoutMs: 500,
})
```

The agent receives a JSON `POST /decide` with:
- `agent_id` — entity ID
- `tick` — current tick
- `perception` — map of evaluated query results
- `available_actions` — list of `{name, params}` the agent can invoke
- `history` — recent action outcomes

It returns `{"actions": [{"name": "fish", "params": {}}]}`.

**Visibility** — control what agents see about scoring:

```go
w.Score(func(w *we.World, agentID string) float64 { ... })
w.ScoreVisibility(we.Public)    // agent sees /score/current every tick
w.ScoreVisibility(we.Hints)     // agent sees /score/hint (text description only)
w.ScoreVisibility(we.Hidden)    // agent sees nothing about score (default)
w.ScoreHint("Maximise total fish landed")  // sets Hints mode and sets the text
```

**Hiding resources from agents**:

```go
lake.Hidden("fish_stock")    // other entities cannot query this resource
lake.Private("fish_stock")   // only the entity itself can read it
```

---

## 7. Scoring

```go
// Terminal: evaluated once at end of Run()
w.Score(func(w *we.World, agentID string) float64 {
    return w.Entity(agentID).Get("gold")
})

// Continuous: sampled every tick, aggregated to a single score at end
w.ScoreContinuous(func(w *we.World, agentID string, tick int) float64 {
    return w.Entity(agentID).Get("health")
}, we.AggregateMean)   // or AggregateMean / AggregateMin / AggregateMax / AggregateSum

// Read the final score after Run() completes:
score := w.FinalScore("boat1")
```

Multiple `Score` and `ScoreContinuous` calls are all summed into the final score.

---

## 8. Query language

Queries can be used anywhere (`w.Query(expr)`, `e.Query(expr)`, `Perception` lists).

| Segment | Meaning |
|---|---|
| `/entities` | All entities in the world |
| `/entities[type=Boat]` | Filtered by type name |
| `/entities[type=Boat][cargo>10]` | Type + resource predicate |
| `/self` | The querying entity (only in `Perception` or `e.Query`) |
| `/self/location` | The container of the querying entity |
| `/self/neighbors` | Direct connection neighbours |
| `/self/neighbors[type=Node]` | Filtered neighbours |
| `/self/contains` | Entities inside self |
| `/resources` | All resources of the preceding entity set |
| `/resources/fish_stock` | Single named resource |
| `/params/regen_rate` | Single named param |
| `/@sum` | Aggregate: sum of preceding scalar values |
| `/@mean` | Aggregate: mean |
| `/@min` / `/@max` | Min or max |
| `/@count` | Count of entities in preceding set |

**Examples**:

```
/entities[type=FishingGround]/resources/fish_stock/@sum
/entities[type=Boat][cargo>0]/resources/cargo/@mean
/self/neighbors[type=Port]/resources/gold
/self/location/resources/capacity
```

---

## 9. Lifecycle — spawning and destroying during simulation

```go
// From a tick function — deferred until end of tick
e.Spawn("child_id", "Bullet", we.Init{Location: e.ID()})
e.Destroy("old_entity_id")
e.DestroySelf()
```

Spawns and destroys are queued and processed after all tick functions complete.
An entity destroyed this tick still finishes its tick function normally.

---

## 10. Run log

```go
w := we.New(we.Config{
    Log: we.LogConfig{
        Enabled:          true,
        Dir:              "./runs",
        SnapshotInterval: 100,   // full snapshot every 100 ticks
    },
})
w.Run()

path := w.RunLogPath()    // "./runs/my-world-20060102-150405.db"

// Reconstruct state at any past tick (after Run):
log, _ := we.OpenRunLog(path)
state, _ := log.StateAt(150)   // nearest snapshot + event replay
```

---

## 11. Tournament

```go
tr := we.NewTournament(we.TournamentConfig{
    Name:         "fishing-benchmark",
    RunsPerWorld: 10,
    Aggregation:  we.AggregateMean,
})

tr.AddWorld("atlantic", func() *we.World { return buildAtlanticWorld() })
tr.AddWorld("pacific",  func() *we.World { return buildPacificWorld() })

// Each agent's endpoint is injected as the "player" provider
tr.AddAgent("claude-3-7", we.ProviderConfig{Endpoint: "http://localhost:8080", TimeoutMs: 500})
tr.AddAgent("gpt-4o",     we.ProviderConfig{Endpoint: "http://localhost:8081", TimeoutMs: 500})

results := tr.Run()
// results.Leaderboard[0] is the top agent (descending mean score)
for _, e := range results.Leaderboard {
    fmt.Printf("%s: %.2f\n", e.AgentName, e.Score)
}
```

World factory functions must register their agent entity types with
`Provider: "player"`. The tournament runner sets `w.Provider("player", agentCfg)`
before each run.

```go
func buildWorld() *we.World {
    w := we.New(we.Config{MaxTicks: 100})
    bot := w.Type("Bot")
    bot.Agent(we.AgentConfig{Provider: "player", Perception: []string{"/self/resources"}})
    // ...
    return w
}
```

---

## 12. MCP server (for LLM-driven simulation)

```go
srv := we.NewMCPServer(world)
srv.ServeStdio(ctx)
```

Exposes tools: `step`, `step_back`, `run`, `pause`, `resume`, `query`,
`list_entities`, `set_resource`, `snapshot`, `restore`, `get_events`,
`get_state_at_tick`, `load_log`, `unload_log`, `list_runs`, `run_tournament`.

---

## 13. Worked example 1 — resource exchange (no agents)

A market with buyers and sellers exchanging goods each tick.

```go
package main

import (
    "fmt"
    we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

func main() {
    w := we.New(we.Config{MaxTicks: 20})

    // Market sits at the top level; holds the exchange actions.
    market := w.Type("Market")
    market.Resources(we.P{"trades": 0.0})

    // RemoteAction: buyer and seller are siblings inside the market.
    market.RemoteAction("buy", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
        price := target.Get("ask_price")
        if invoker.Get("gold") < price {
            return we.Fail("insufficient funds")
        }
        invoker.Set("gold", invoker.Get("gold")-price)
        invoker.Set("items", invoker.Get("items")+1)
        target.Set("trades", target.Get("trades")+1)
        return we.OK()
    })

    // Buyer tries to buy every tick.
    buyer := w.Type("Buyer")
    buyer.Resources(we.P{"gold": 50.0, "items": 0.0})
    buyer.Tick(func(e *we.Entity, dt float64) {
        e.Act("buy", we.P{})  // targets containing market
    })

    w.Spawn("market1", "Market", we.Init{Resources: we.P{"ask_price": 5.0}})
    w.Spawn("buyer1", "Buyer", we.Init{Location: "market1"})

    w.Run()

    fmt.Println("items bought:", w.Entity("buyer1").Get("items"))
    fmt.Println("trades recorded:", w.Entity("market1").Get("trades"))
}
```

---

## 14. Worked example 2 — agent-backed fishing world

An LLM agent controls a boat; the world tracks fish regeneration and scoring.

```go
package main

import (
    we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

func main() {
    w := we.New(we.Config{
        Name:     "fishing",
        MaxTicks: 365,
        DT:       1.0,
        Log:      we.LogConfig{Enabled: true, Dir: "./runs", SnapshotInterval: 100},
    })

    // Fishing ground: fish regenerate each tick; "fish" action consumes stock.
    ground := w.Type("FishingGround")
    ground.Params(we.P{"max_stock": 1000.0, "regen": 5.0})
    ground.Resources(we.P{"fish_stock": 500.0})
    ground.Hidden("fish_stock")  // agent cannot observe this directly
    ground.Tick(func(e *we.Entity, dt float64) {
        stock := e.Get("fish_stock") + e.Param("regen")*dt
        if stock > e.Param("max_stock") {
            stock = e.Param("max_stock")
        }
        e.Set("fish_stock", stock)
    })
    // Agent calls "fish"; this executes on the ground (the invoker's container).
    ground.Action("fish", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
        take := 10.0
        if target.Get("fish_stock") < take {
            return we.Fail("not enough fish")
        }
        target.Set("fish_stock", target.Get("fish_stock")-take)
        invoker.Set("cargo", invoker.Get("cargo")+take)
        return we.OK()
    })

    // Port: agent calls "unload" to convert cargo to gold.
    port := w.Type("Port")
    port.Resources(we.P{"gold_paid": 0.0})
    port.Action("unload", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
        cargo := invoker.Get("cargo")
        if cargo <= 0 {
            return we.Fail("no cargo")
        }
        invoker.Set("cargo", 0)
        invoker.Set("gold", invoker.Get("gold")+cargo*2)
        target.Set("gold_paid", target.Get("gold_paid")+cargo*2)
        return we.OK()
    })

    // Boat: agent-backed. Perception covers its own state and nearby locations.
    boat := w.Type("Boat")
    boat.Params(we.P{"fuel_capacity": 100.0})
    boat.Resources(we.P{"cargo": 0.0, "gold": 0.0, "fuel": 100.0})
    boat.Agent(we.AgentConfig{
        Provider: "claude",
        Prompt:   "You are a fishing boat. Fish to collect cargo; return to port to unload for gold. Manage fuel.",
        Perception: []string{
            "/self/resources",
            "/self/location/resources",
        },
        TickFrequency: 1,
    })

    w.Provider("claude", we.ProviderConfig{
        Endpoint:  "http://localhost:8080",
        TimeoutMs: 500,
    })

    // Score: total gold earned by the agent.
    w.Score(func(w *we.World, agentID string) float64 {
        return w.Entity(agentID).Get("gold")
    })
    w.ScoreHint("Maximise gold earned by fishing and unloading cargo")

    w.Spawn("ground1", "FishingGround", we.Init{})
    w.Spawn("port1",   "Port",          we.Init{})
    w.Spawn("boat1",   "Boat",          we.Init{Location: "ground1"})

    w.Run()
}
```

---

## 15. Common mistakes

| Mistake | Fix |
|---|---|
| `"fuel": 0` in `we.P{}` | Use `"fuel": 0.0` — all scalars must be `float64` |
| `Action` between siblings | Use `RemoteAction` — `Action` only works parent↔child |
| Reading action result in same tick | Actions are deferred; result is visible next tick |
| `e.Neighbors()` returns nothing for an entity inside a container | The entity itself has no edges; call `e.Location().Neighbors(...)` to traverse the container's graph |
| Spawning with `Location` that doesn't exist yet | Spawn containers before the entities placed inside them |
| Forgetting `Provider: "player"` in tournament world factories | Each tournament world factory must use `Provider: "player"` so the runner can inject agents |
| Continuous score samples `[0]` at tick 0 before any tick runs | Record your first sample from tick 1 if you need a non-zero baseline |
