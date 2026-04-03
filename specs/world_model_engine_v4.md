# World Model Engine — Design Specification v4

## 1. Introduction

A general-purpose world-model simulation engine designed around a core challenge: **can an agent learn to thrive in a world it has never seen before?**

World authors define arbitrary worlds — economies, ecosystems, dungeons, political systems, physics puzzles — using a Go library. Agent developers build agents that are dropped into these worlds blind and must discover the rules, form strategies, and survive. An agent that can thrive across many diverse worlds is demonstrating something close to general intelligence. This is the engine's north star: a competitive, open-ended AGI test.

The engine is a Go library (`worldengine`) that provides a small, composable API for defining entity types, spawning instances, wiring up connections, and running simulations. World definitions are Go programs. This means full language expressiveness, real type safety, real tooling, and zero impedance mismatch between behavior logic and the engine.

The system is designed to scale to thousands of entities over thousands of iterations.

### 1.1 Design Targets

- **Scale**: 10,000+ entities, 10,000+ ticks per run, many runs in sequence
- **Speed**: All entity logic is native Go; no interpreted runtime in the hot path
- **Agent-agnostic**: External agents connect via a standard HTTP API — any LLM, any harness, any language
- **Expressiveness**: Full Go for world logic — conditionals, closures, composition, anything the language supports
- **Simplicity**: The library API is small enough for an LLM to learn from a few examples
- **Observability**: Every state mutation is logged as a delta — full simulation history is reconstructable from a single file
- **AGI test**: The generative space of possible worlds creates an open-ended, adversarial benchmark that resists overfitting

---

## 2. Core Design Principles

### 2.1 Entities as the Universal Abstraction

Everything in the world is an Entity. An Entity has:

- **Resources** — mutable state (numbers, strings, booleans, lists, sets, queues, maps)
- **Parameters** — static configuration (immutable during simulation)
- **Tick logic** — a function executed every simulation step
- **Actions** — named behaviors invokable by any entity
- **Connections** — typed, weighted edges to other entities
- **Composition** — entities can contain other entities

An entity becomes an "agent" simply by invoking actions from its tick function, or by being backed by an external agent provider. There is no separate agent type.

### 2.2 Types and Instances

Types define schema (params, resources) and behavior (tick function, actions). Instances are created from types with specific initial values. Many instances can share one type. This maps naturally to Go: a type is a set of function registrations, an instance is a call to `Spawn()`.

### 2.3 Go Library, Not a DSL

World definitions are Go programs that import and use the `worldengine` package. This eliminates the impedance mismatch of embedding code in JSON, gives world authors the full Go language, and means LLMs generate real, compilable, type-checked code rather than hoping JSON string escaping works.

### 2.4 ECS-Inspired Storage

Entity state is stored internally in flat, contiguous property tables indexed by entity ID — not as scattered objects. The library API presents an entity-shaped facade (`e.Get()`, `e.Set()`) while the engine manages cache-friendly columnar storage underneath. This is invisible to the world author but critical for performance at scale.

---

## 3. Library API

### 3.1 World Configuration

```go
package main

import (
    "math"
    we "worldengine"
)

func main() {
    w := we.New(we.Config{
        DT:       1.0,
        TickUnit: "day",
        MaxTicks: 365,
        MaxActionsPerTick: 3,
        Log: we.LogConfig{
            Dir:              "./runs",        // directory for run log files
            SnapshotInterval: 100,             // full state snapshot every N ticks
        },
    })

    // ... define types, spawn entities, run ...

    w.Run()
}
```

### 3.2 Defining Types

A type defines the schema and behavior for a category of entity.

```go
ground := w.Type("FishingGround")

ground.Params(we.P{
    "max_fish_stock": 2000.0,
    "regen_rate":     5.0,
})

ground.Resources(we.P{
    "fish_stock": 0.0,
})

// Mark fish_stock as hidden — agents can't see it on other entities
ground.Hidden("fish_stock")

ground.Tick(func(e *we.Entity, dt float64) {
    stock := e.Get("fish_stock")
    max := e.Param("max_fish_stock")
    rate := e.Param("regen_rate")
    e.Set("fish_stock", math.Min(max, stock+rate*dt))
})

ground.Action("fish", func(target *we.Entity, invoker *we.Entity, p we.P) {
    skill := p.Float("skill")
    caught := skill * 0.3

    // Check invoker can pay the cost — GetOr returns 0 for missing resources
    if invoker.GetOr("fuel", 0) < 1.0 {
        return // not enough fuel to fish
    }

    // Update the fishing ground
    target.Set("fish_stock", math.Max(0, target.Get("fish_stock")-skill*0.5))

    // Update the invoker
    invoker.Set("catch", invoker.Get("catch")+caught)
    invoker.Set("fuel", invoker.Get("fuel")-1.0)
})
```

### 3.3 NPC Agents with Conditional Logic

Because behavior is Go code, complex NPC logic is natural:

```go
npc := w.Type("NPCBoat")

npc.Resources(we.P{
    "fuel": 100.0, "catch": 0.0, "aggression": 0.5,
})

npc.Tick(func(e *we.Entity, dt float64) {
    // Burn fuel
    e.Set("fuel", e.Get("fuel")-dt*0.5)

    // Low fuel — find a port and refuel
    if e.Get("fuel") < 10 {
        port := e.Neighbors(we.Filter{Type: "Port"}).Nearest()
        if port != nil {
            e.Act("move", we.P{"target": port.ID()})
            e.Act("refuel", we.P{"amount": 50})
        }
        return
    }

    // Decide whether to fish or explore
    loc := e.Location()
    if loc != nil && loc.Get("fish_stock") > 500 {
        e.Act("fish", we.P{"skill": e.Get("aggression") * 10})
    } else {
        // Move to the richest neighboring fishing ground
        grounds := e.Neighbors(we.Filter{Type: "FishingGround"})
        best := grounds.MaxBy("fish_stock")
        if best != nil {
            e.Act("move", we.P{"target": best.ID()})
        }
    }
})
```

### 3.4 External Agent Types

For entities backed by an LLM or other external agent:

```go
player := w.Type("PlayerBoat")

player.Resources(we.P{
    "fuel": 100.0, "catch": 0.0, "money": 1000.0,
})

player.Agent(we.AgentConfig{
    Provider: "claude",
    Prompt:   "You are a fishing boat captain. Maximize profit over the season.",
    Perception: []string{
        "/self",
        "/self/available_actions",
        "/self/location",
        "/self/location/neighbors",
        "/self/location/contains",
    },
})
```

A type can have both a `Tick` function and an `Agent` config. If both are present, the tick function runs first (for passive state updates like fuel burn), then the agent is invoked for decisions.

### 3.5 Spawning Instances

```go
w.Spawn("lake_alpha", "FishingGround", we.Init{
    Params:    we.P{"max_fish_stock": 2000, "regen_rate": 5},
    Resources: we.P{"fish_stock": 1200},
})

w.Spawn("lake_beta", "FishingGround", we.Init{
    Params:    we.P{"max_fish_stock": 800, "regen_rate": 2},
    Resources: we.P{"fish_stock": 800},
})

w.Spawn("port_alpha", "Port", we.Init{
    Params:    we.P{"fuel_price": 2.5},
    Resources: we.P{"fuel_supply": 5000},
})

w.Spawn("boat_1", "PlayerBoat", we.Init{
    Resources: we.P{"fuel": 80, "money": 1000},
})

w.Spawn("npc_1", "NPCBoat", we.Init{
    Resources: we.P{"fuel": 90, "aggression": 0.7},
})
```

Any param or resource not specified in the `Init` uses the type's default.

### 3.6 Connections

Connections are declared on the world, not on entities. This makes the topology visible in one place:

```go
w.Connect("lake_alpha", "port_alpha", "shore_access", 1.0)
w.Connect("port_alpha", "port_beta", "sea_route", 50.0)
w.Connect("lake_beta", "port_beta", "shore_access", 1.0)
```

Connections are bidirectional by default. For directional connections:

```go
w.ConnectDirected("river_source", "river_mouth", "downstream", 1.0)
```

### 3.7 Placement and Composition

Entities can be placed inside other entities:

```go
w.Place("boat_1", "lake_alpha")
w.Place("npc_1", "lake_alpha")
w.Place("crane_1", "port_alpha")
```

Placement is a runtime relationship — entities can move between containers via the `move` action.

### 3.8 Entity API Reference

The `*Entity` passed to tick and action functions:

```go
// State access
e.Get("fuel") float64             // get a resource value (panics if missing)
e.GetOr("fuel", 0.0) float64     // get a resource value with default (returns default if missing)
e.Set("fuel", 80.0)               // set a resource value
e.Has("fuel") bool                // check if a resource exists
e.Param("speed") float64          // get an immutable parameter
e.ID() string                     // entity ID
e.Type() string                   // type name

// Complex resource types
e.ListPush("cargo", item)
e.ListPop("cargo") any
e.QueuePush("arrivals", item)
e.QueuePop("arrivals") any
e.SetAdd("visited", "port_alpha")
e.SetHas("visited", "port_alpha") bool
e.MapSet("prices", "fish", 12.5)
e.MapGet("prices", "fish") any

// Spatial awareness
e.Location() *Entity              // the entity this one is inside
e.Contains() EntitySet            // entities inside this one
e.Neighbors(filter ...Filter) EntitySet  // connected entities

// Actions
e.Act(name string, params P)      // invoke an action (on self or target)
```

### 3.9 EntitySet API

Query results and collection operations:

```go
set.Filter(Filter{Type: "Port"}) EntitySet
set.MaxBy("fish_stock") *Entity
set.MinBy("fuel") *Entity
set.Nearest() *Entity              // by connection weight
set.Count() int
set.Each(func(e *Entity))
set.Sum("resource_name") float64
set.Avg("resource_name") float64
```

---

## 4. Resource Model

### 4.1 Supported Types

| Type | Go Type | Example |
|------|---------|---------|
| number | `float64` | `"fuel": 100.0` |
| string | `string` | `"status": "idle"` |
| boolean | `bool` | `"docked": true` |
| list | `[]any` | `"cargo": []` |
| set | `Set` | `"visited": set()` |
| queue | `Queue` | `"arrivals": queue()` |
| map | `map[string]any` | `"prices": {}` |

Types are inferred from the default values provided in the type definition. `e.Get()` and `e.Set()` handle the common `float64` case; complex types use dedicated methods (`ListPush`, `QueuePop`, `MapSet`, etc.).

### 4.2 Resource Visibility

Resources can be marked with a visibility level that controls what external agents can see when querying other entities:

```go
ground.Hidden("fish_stock")    // invisible to other entities' perception
ground.Private("internal_id")  // visible only to the owning entity's tick function
```

By default, resources are **public** — visible to any entity or agent that can query the owning entity. Visibility levels:

| Level | Tick function (self) | Tick function (other entity) | Agent perception |
|-------|---------------------|------------------------------|-----------------|
| **Public** (default) | Yes | Yes | Yes |
| **Hidden** | Yes | Yes | No |
| **Private** | Yes | No | No |

Hidden resources are the primary tool for information asymmetry in the game mode. A fishing ground's `fish_stock` being hidden means agents must infer stock levels from action feedback (catch rates declining) rather than reading the value directly. This makes exploration and hypothesis formation genuine challenges.

Note: visibility is enforced by the perception system and query engine. Compiled tick functions running inside the engine can always read any entity's state — visibility is about what information leaves the engine via the agent provider interface.

### 4.3 Property Table Storage

Internally, each resource field maps to a typed property table — a contiguous array indexed by entity ID. This is invisible to the world author but enables cache-friendly iteration, cheap snapshots, and efficient rollback for sensitivity analysis.

---

## 5. Connection Graph

### 5.1 Typed, Weighted Edges

Entities are connected by typed, weighted edges forming a graph distinct from the containment hierarchy:

```go
w.Connect("lake_alpha", "port_alpha", "shore_access", 1.0)
w.Connect("port_alpha", "port_beta", "sea_route", 50.0)
```

Weights can represent distance, cost, time, or any domain-appropriate metric.

### 5.2 Graph Storage

Connections are stored as adjacency lists indexed by entity ID, with secondary indexes on connection type for efficient filtered traversal.

### 5.3 Connection Descriptions

By default, agents see only that a connection exists and where it leads — not its type, weight, or any metadata. The world author can register a function that controls what description agents receive for each connection:

```go
w.ConnectionDescription(func(conn we.Connection) string {
    switch conn.Type {
    case "sea_route":
        return "A well-traveled sea route"
    case "shore_access":
        return "A short path to the shore"
    default:
        return "" // no description — agent sees only the destination
    }
})
```

The function returns an unstructured string — it could be natural language, JSON, or anything else. It's up to the agent to interpret it. The function receives the full `Connection` struct (type, weight, endpoints) but can choose to reveal as little or as much as the world author wants. This keeps information asymmetry in the world author's hands and forces agents to learn through experience.

Compiled NPC tick functions running inside the engine can always access the full connection data directly.

### 5.4 Mutable Connections

The connection graph is mutable during simulation:

```go
// Inside a tick function
e.ConnectTo("new_ally", "alliance", 1.0)
e.Disconnect("former_ally", "alliance")
```

---

## 6. Actions

### 6.1 Two-Party Action Pattern

Every action is a two-party transaction. The action handler receives the **target entity** (the one the action is defined on), the **invoking entity** (whoever called `e.Act()`), and the action parameters:

```go
port.Action("refuel", func(target *we.Entity, invoker *we.Entity, p we.P) {
    amount := p.Float("amount")
    price := target.Param("fuel_price")
    cost := amount * price

    // Check the invoker can afford it
    if invoker.GetOr("money", 0) < cost {
        return // can't afford — action fails silently
    }

    // Update both parties
    target.Set("fuel_supply", target.Get("fuel_supply")-amount)
    invoker.Set("fuel", invoker.Get("fuel")+amount)
    invoker.Set("money", invoker.Get("money")-cost)
})
```

For self-targeted actions (when no `"target"` is specified), `target` and `invoker` are the same entity.

### 6.2 Resource Availability as a Natural Guard

Actions should use `GetOr` to check whether the invoker has the required resources. An entity that lacks a resource gets the default value (typically 0), which means it naturally fails the availability check. This handles three cases uniformly:

- The invoker has the resource but not enough → action fails
- The invoker has the resource at zero → action fails
- The invoker doesn't have that resource type at all → `GetOr` returns 0 → action fails

This means world authors don't need to worry about what type of entity invokes an action. A drone with no `money` resource can try to refuel; it just silently fails because `GetOr("money", 0)` returns 0, which is less than any positive cost. No runtime errors, no special casing.

### 6.3 Action Locality

Actions are **local** by default — the invoker must be colocated with the target. Specifically, the invoker must either be *inside* the target (e.g., a boat inside a lake) or the target must be *inside* the invoker (e.g., cargo inside a ship). This makes spatial positioning meaningful: you have to *be somewhere* to interact with it.

Actions can be declared **remote** to bypass this constraint:

```go
// Local action (default) — invoker must be colocated with target
port.Action("refuel", func(target *we.Entity, invoker *we.Entity, p we.P) {
    // ...
})

// Remote action — can be invoked from anywhere if the invoker has a reference
beacon.RemoteAction("distress_signal", func(target *we.Entity, invoker *we.Entity, p we.P) {
    target.Set("alert_level", target.Get("alert_level")+1)
    // The beacon records who signaled — but the invoker doesn't need to be nearby
})
```

Remote actions enable signaling, broadcasting, and coordination across distance. A market entity might expose a remote `place_order` action. A command center might accept remote `report` actions. The distinction keeps most world interactions spatially grounded while allowing deliberate exceptions.

### 6.4 Invocation

Tick functions invoke actions via `e.Act()`:

```go
e.Act("fish", we.P{"skill": 5})                             // act on self/location
e.Act("refuel", we.P{"amount": 50, "target": "port_alpha"}) // act on a specific target
```

When no `"target"` is specified, the engine resolves the action against the invoker's current location or the invoker itself. When a `"target"` is specified, the engine resolves the target entity by ID. For local actions, the engine verifies colocation before executing; if the invoker is not colocated, the action fails.

### 6.5 Action Feedback

Actions return a result indicating success or failure:

```go
ground.Action("fish", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
    skill := p.Float("skill")

    if invoker.GetOr("fuel", 0) < 1.0 {
        return we.Fail("insufficient resources")
    }

    stock := target.Get("fish_stock")
    if stock <= 0 {
        return we.Fail("nothing to catch")
    }

    caught := math.Min(skill*0.3, stock)
    target.Set("fish_stock", stock-caught)
    invoker.Set("catch", invoker.Get("catch")+caught)
    invoker.Set("fuel", invoker.Get("fuel")-1.0)

    return we.OK()
})
```

Action results are recorded in the event log and included in the `history` field of the agent provider request. The world author controls how much detail to include in failure reasons — they may give specific feedback (`"not enough money"`) or vague feedback (`"action failed"`) or no reason at all (`we.Fail("")`), forcing the agent to infer what went wrong.

For external agents, the history field shows:

```json
"history": [
    { "tick": 41, "actions": [{ "name": "fish", "params": { "skill": 5 } }], "result": "ok" },
    { "tick": 42, "actions": [{ "name": "fish", "params": { "skill": 5 } }], "result": "failed", "reason": "nothing to catch" }
]
```

### 6.6 Built-In Actions

The engine provides `move` as a built-in action available to all entities:

```go
e.Act("move", we.P{"target": "port_alpha"})
```

The engine validates that the target is reachable (connected to the entity's current location), applies the world's movement cost function if one is registered, and moves the entity. If the cost function returns `false` (e.g., not enough fuel), the move fails and the entity stays put.

If a type defines its own `move` action, it overrides the built-in for that type.

### 6.7 Movement Cost

The world author can register a global movement cost function:

```go
w.MovementCost(func(mover *we.Entity, conn we.Connection) bool {
    fuelCost := conn.Weight * 0.1
    if mover.GetOr("fuel", 0) < fuelCost {
        return false // not enough fuel — move fails
    }
    mover.Set("fuel", mover.Get("fuel")-fuelCost)
    return true // move succeeds
})
```

This keeps movement economics in one place rather than scattered across every entity type. A world with no movement cost function gets free movement. The `Connection` struct provides `Type`, `Weight`, `From`, and `To` for the cost function to use.

### 6.8 Entity Lifecycle

Entities can be created and destroyed during simulation:

```go
// Inside a tick function — spawn a new entity
e.Spawn("order_123", "Order", we.Init{
    Resources: we.P{"quantity": 50, "price": 12.5},
    Location:  "market_alpha",
})

// Destroy an entity
e.Destroy("order_123")

// Destroy self
e.DestroySelf()
```

Spawned entities must reference an existing type, and optionally specify a location (which entity to place them inside). Destroyed entities are removed from all property tables, the connection graph, and their container at end of tick.

This enables worlds where entities are created dynamically: markets that spawn order entities, factories that produce goods, agents that die when health reaches zero, ecosystems where organisms reproduce.

### 6.9 Actions Per Tick

A tick function may invoke zero or more actions. Actions are collected and executed sequentially after all tick functions complete. The `MaxActionsPerTick` config parameter enforces a limit when needed.

---

## 7. Tick Model

### 7.1 Tick Loop

Each tick:

1. Execute tick logic for all entities (collects invoked actions)
2. Dispatch agent provider calls for agent-backed entities (collects actions)
3. Merge and order all actions deterministically
4. Execute actions sequentially — resolving target and invoker for each
5. Process transitions (entity movement via built-in `move`)
6. Evaluate scoring functions (if registered)
7. Commit state changes
8. Write events to run log (deltas, actions, scores, lifecycle events)
9. Write snapshot if tick is a snapshot interval boundary

### 7.2 Execution Order

Entities are processed in deterministic order (lexicographic by entity ID). Cross-entity action ordering follows the same deterministic ordering.

### 7.3 Tick Granularity

The `dt` parameter represents the duration of one tick in world-defined units. A tick could be a second, a day, or a year — the interpretation is entirely up to the world author.

Tick granularity is a **modeling accuracy dimension**: too coarse and the simulation misses dynamics; too fine and it wastes compute.

### 7.4 Parallelism

Tick functions for entities that don't share property tables can execute in parallel. The engine performs a dependency analysis pass at startup and assigns entities to goroutine pools accordingly.

---

## 8. Query Language

### 8.1 Purpose

A concise, path-based query language for selecting entities, traversing properties, walking the connection graph, and aggregating results. Used by:

- **Agent perception** — scoping what context an external agent receives
- **Scoring functions** — reading arbitrary world state
- **MCP tools** — general-purpose world inspection
- **Programmatic queries** — available in Go via `w.Query()`

### 8.2 Syntax

```
// Selecting entities
/entities/lake_alpha
/entities[type=FishingGround]
/entities[resources.fuel > 50]

// Traversing properties
/entities/lake_alpha/resources/fish_stock
/entities/lake_alpha/params/regen_rate
/entities/lake_alpha/contains

// Graph traversal
/entities/boat_1/location
/entities/boat_1/location/neighbors
/entities/boat_1/location/neighbors/contains[type=Ship]

// Depth-limited traversal
/entities/port_alpha/neighbors(depth=2)
/entities/port_alpha/neighbors(type=sea_route, depth=3)

// Wildcards
/entities/*/resources/fuel
/entities[type=Port]/contains[type=Container]/resources

// Aggregation
/entities[type=FishingGround]/resources/fish_stock/@sum
/entities[type=Port]/contains/@count
/entities[type=Ship]/resources/fuel/@min
```

### 8.3 Grammar Elements

| Element | Meaning |
|---------|---------|
| `/entities/id` | Select entity by ID |
| `/entities[predicate]` | Filter entities by predicate |
| `/resources/name` | Access a resource property |
| `/params/name` | Access a parameter |
| `/contains` | Descend into contained entities |
| `/neighbors` | Traverse connection graph |
| `/neighbors(depth=N)` | Depth-limited graph traversal |
| `/neighbors(type=T)` | Type-filtered graph traversal |
| `@sum`, `@count`, `@min`, `@max`, `@avg` | Aggregation functions |
| `*` | Wildcard |

### 8.4 Usage in Go

```go
// In world setup or scoring
totalFish := w.Query("/entities[type=FishingGround]/resources/fish_stock/@sum")
activePorts := w.Query("/entities[type=Port, resources.fuel_supply > 0]/@count")

// In tick functions (scoped to what the entity can see)
localBoats := e.Query("/location/contains[type=FishingBoat]/@count")
```

### 8.5 Implementation

Hand-written recursive descent parser in Go. The grammar is small enough that a parser generator would be overkill. An index on entity type is maintained for efficient filtered queries.

The query language is for **selecting and traversing**. It deliberately excludes arithmetic and Turing-complete computation — that's Go's job.

---

## 9. Agent Provider Interface

### 9.1 Overview

The engine invokes external agents — LLMs, custom bots, human players — via a standard HTTP contract. This is the reverse of MCP: instead of an external system calling the engine, the engine calls the agent. Any agent provider that speaks HTTP can participate.

### 9.2 The Contract

**Request (engine → agent):**

```json
POST /decide
{
  "agent_id": "boat_1",
  "tick": 42,
  "perception": {
    "/self": { "fuel": 80, "catch": 12, "money": 430 },
    "/self/location": { "id": "lake_alpha", "type": "FishingGround", "resources": { "fish_stock": 890 } },
    "/self/location/neighbors": [
      { "id": "port_alpha", "type": "Port", "connection": { "type": "shore_access", "weight": 1.0 } }
    ],
    "/self/location/contains": [
      { "id": "boat_2", "type": "FishingBoat", "resources": { "fuel": 40, "catch": 25 } }
    ]
  },
  "available_actions": [
    { "name": "fish", "params": [{ "name": "skill", "type": "number" }] },
    { "name": "move", "params": [{ "name": "target", "type": "entity_id" }] },
    { "name": "idle" }
  ],
  "system_prompt": "You are a fishing boat captain. Maximize profit over the season.",
  "history": [
    { "tick": 41, "actions": [{ "name": "fish", "params": { "skill": 5 } }], "result": "ok" },
    { "tick": 40, "actions": [{ "name": "move", "params": { "target": "lake_beta" } }], "result": "failed", "reason": "insufficient resources" }
  ]
}
```

**Response (agent → engine):**

```json
{
  "actions": [
    { "name": "fish", "params": { "skill": 5 } }
  ]
}
```

An empty actions list means the agent does nothing this tick.

### 9.3 Provider Configuration

Providers are registered on the world:

```go
w.Provider("claude", we.ProviderConfig{
    Endpoint:  "http://localhost:8090/decide",
    AuthType:  "bearer",
    TokenEnv:  "CLAUDE_API_KEY",
    TimeoutMs: 10000,
    Retries:   1,
})

w.Provider("ollama", we.ProviderConfig{
    Endpoint:  "http://localhost:11434/decide",
    TimeoutMs: 30000,
})

w.Provider("channels", we.ProviderConfig{
    Endpoint:  "http://localhost:8091/decide",
    AuthType:  "bearer",
    TokenEnv:  "CHANNELS_TOKEN",
    TimeoutMs: 60000,
})
```

### 9.4 Proxy Architecture

The engine always calls the standard Agent Provider Interface. **Proxy adapters** translate between the standard contract and specific backends:

```
Engine  →  Agent Provider Interface (HTTP)  →  Proxy Adapter  →  Backend
                                                    ↓
                                          ┌─────────────────────┐
                                          │ claude-api-proxy     │  → Anthropic API
                                          │ openai-proxy         │  → OpenAI API
                                          │ ollama-proxy         │  → Ollama (local)
                                          │ channels-proxy       │  → Claude Code Channels
                                          │ custom               │  → Any HTTP endpoint
                                          └─────────────────────┘
```

Each proxy is a lightweight HTTP server that translates the decision request into the backend's format, handles authentication, and parses the response back into the standard action list. Adding a new LLM provider means writing a proxy, not modifying the engine.

### 9.5 Claude Code Channels as a Backend

Claude Code Channels is particularly interesting for the game mode. A Claude Code session running locally has persistent context across ticks, access to filesystem and tools, and uses plan-based pricing rather than per-token billing. The agent could write and execute analysis code, maintain a local knowledge base, or use MCP tools — capabilities that go well beyond a stateless API call.

### 9.6 Perception System

The perception block on an agent type uses query language expressions to scope what context the agent receives:

```go
player.Agent(we.AgentConfig{
    Provider: "claude",
    Prompt:   "You are a fishing boat captain...",
    Perception: []string{
        "/self",
        "/self/available_actions",
        "/self/location",
        "/self/location/neighbors",
    },
})
```

The engine evaluates these queries at decision time, assembles the results into the `perception` field of the HTTP request, and includes the available actions list.

### 9.7 Discovery API

In game mode, agents need to discover the world without prior knowledge. These queries are always available regardless of perception configuration:

| Query | Returns |
|-------|---------|
| `/self` | The agent's own full state |
| `/self/available_actions` | Actions the agent can currently invoke |
| `/self/location` | The entity the agent is inside (public resources only) |
| `/self/location/entities` | All entities at the agent's current location (public resources only) |
| `/self/connections` | The agent's direct connections (with descriptions if configured) |
| `/world/config` | World configuration: `MaxTicks`, `MaxActionsPerTick`, `TickUnit`, current tick |

Broader perception (neighbors, type filtering, deeper traversal) must be granted in the perception config.

**Hidden resource enforcement:** When the perception system assembles context for an agent, it strips all hidden and private resources from other entities. The agent sees only public resources. This means a fishing ground perceived by an agent might appear as `{"id": "lake_alpha", "type": "FishingGround"}` with no resource data at all if `fish_stock` is hidden — the agent knows the lake exists but not how much fish it has.

### 9.8 Performance

- **Parallel dispatch**: All agent decisions for a tick are dispatched concurrently
- **Timeouts**: Per-provider configurable; exceeded = no actions this tick
- **Tick frequency**: `TickFrequency: 5` on an agent config means the agent is only consulted every 5 ticks
- **Hybrid execution**: Most entities run native Go tick functions; only designated agents make external calls

### 9.9 MCP Server Interface

The engine also runs as an MCP server using the official Go SDK (`github.com/modelcontextprotocol/go-sdk/mcp`), allowing external LLMs to orchestrate simulations and tournaments.

**Tools exposed:**

| Tool | Description |
|------|-------------|
| `step` | Advance the simulation by N ticks |
| `step_back` | Reconstruct state at a previous tick (snapshot + delta replay) |
| `run` | Run until a condition is met or max ticks reached |
| `pause` | Pause a running simulation |
| `resume` | Resume a paused simulation |
| `query` | Execute a query language expression |
| `snapshot` | Save the current world state |
| `restore` | Restore a previous snapshot |
| `set_resource` | Modify an entity's resource |
| `list_entities` | List entities with optional type filter |
| `get_events` | Retrieve events from the run log for a tick range |
| `get_state_at_tick` | Reconstruct full world state at an arbitrary tick |
| `load_log` | Load a completed run log (`.db` file) for replay |
| `unload_log` | Close a loaded run log |
| `list_runs` | Enumerate available run log files |
| `run_tournament` | Execute a tournament and return results |

---

## 10. Engine Architecture

### 10.1 Core Components

| Component | Responsibility |
|-----------|---------------|
| **World** | Top-level container; holds types, instances, config, and runs the simulation |
| **Type Registry** | Maps type names to their tick functions, action handlers, and schema |
| **Property Tables** | ECS-style columnar storage for all entity state |
| **Connection Graph** | Adjacency lists with type indexes |
| **Tick Scheduler** | Deterministic tick loop with parallelism |
| **Action Dispatcher** | Collect and execute actions in deterministic order |
| **Transition Manager** | Handle entity movement between containers |
| **Query Engine** | Parse and execute query language expressions |
| **Run Log** | Write delta events, snapshots, and scores to per-run SQLite database |
| **State Reconstructor** | Rebuild world state at any tick from snapshots + deltas |
| **Agent Dispatcher** | Assemble perception, call agent providers in parallel, parse responses |
| **MCP Server** | Expose engine capabilities over MCP |
| **Tournament Runner** | Run agents against world corpora, aggregate scores, produce leaderboards |

### 10.2 Tick Execution Detail

```go
for tick := 0; tick < maxTicks; tick++ {
    // Phase 1: Execute all tick functions (parallel where safe)
    tickActions := scheduler.ExecuteTicks(tables, dt)

    // Phase 2: Dispatch agent provider calls (parallel)
    agentActions := agentDispatcher.Decide(tables, tick)

    // Phase 3: Merge and order all actions deterministically
    allActions := merge(tickActions, agentActions)

    // Phase 4: Execute actions — resolve target + invoker, call handler
    // All resource mutations during execution are captured as deltas
    for _, action := range allActions {
        target := resolveTarget(action)
        invoker := resolveInvoker(action)
        handler := registry.GetAction(target.Type(), action.Name)
        handler(target, invoker, action.Params)
    }

    // Phase 5: Process built-in moves (with movement cost)
    transitions.Process(tables, connectionGraph, movementCostFn)

    // Phase 6: Evaluate scoring
    scores := scoring.Evaluate(tables, tick)

    // Phase 7: Write events to run log
    runLog.WriteEvents(tick, allActions, scores)

    // Phase 8: Snapshot if on interval boundary
    if tick % config.Log.SnapshotInterval == 0 {
        runLog.WriteSnapshot(tick, tables, connectionGraph)
    }
}
```

### 10.3 No Code Generation

Unlike v2 of this spec, there is no code generation pipeline. World definitions compile as normal Go programs. The library registers tick functions and action handlers at init time; the engine calls them directly. This simplifies the toolchain to: write Go → compile → run.

---

## 11. Event Log & Run Storage

### 11.1 Design Philosophy

Every state mutation in the engine is logged as a delta event. Rather than recording full snapshots of entity state at each tick, the engine writes the minimal change record: which entity, which field, old value, new value, and when. This keeps log size proportional to *activity* rather than *world size* — a 10,000-entity world where only 50 entities change per tick produces 50 delta records, not 10,000 entity snapshots.

Full state at any tick is reconstructable by loading the nearest prior snapshot and replaying deltas forward. This gives the engine (and external tools like UIs) random access to any point in history without the storage cost of per-tick snapshots.

### 11.2 Run Log Format

Each simulation run produces a single SQLite database file. SQLite is chosen over flat formats (CSV, JSON lines) because the log needs to support random access by tick, filtering by entity and event type, and range queries — all of which are trivial with SQL indexes and painful with sequential file formats. A single `.db` file per run preserves the "one file per run" property while providing full query capabilities.

**File naming convention:**

```
{run_dir}/{world_name}_{timestamp}_{run_id}.db
```

Example: `./runs/fishing_economy_20260403T141500_a1b2c3.db`

The `run_id` is a short random suffix for uniqueness when the same world runs multiple times per second (e.g., during tournaments).

### 11.3 Schema

```sql
-- Run-level metadata
CREATE TABLE run_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- Populated at run start:
--   world_name, dt, tick_unit, max_ticks, max_actions_per_tick,
--   snapshot_interval, started_at, engine_version
-- Updated at run end:
--   completed_at, final_tick, status (completed | paused | error)

-- Type definitions — schema and configuration for each entity type
CREATE TABLE types (
    name           TEXT PRIMARY KEY,
    params_schema  TEXT NOT NULL,  -- JSON: { "param_name": default_value, ... }
    resource_schema TEXT NOT NULL, -- JSON: { "resource_name": default_value, ... }
    visibility     TEXT NOT NULL   -- JSON: { "resource_name": "public"|"hidden"|"private", ... }
);

-- Initial entity state at tick 0 (before any tick functions run)
CREATE TABLE initial_entities (
    entity_id TEXT PRIMARY KEY,
    type_name TEXT NOT NULL,
    params    TEXT NOT NULL,  -- JSON object
    resources TEXT NOT NULL,  -- JSON object
    location  TEXT            -- entity ID of container, or NULL
);

-- Initial connection graph at tick 0
CREATE TABLE initial_connections (
    from_id    TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    type       TEXT NOT NULL,
    weight     REAL NOT NULL,
    directed   INTEGER NOT NULL DEFAULT 0,  -- 0 = bidirectional, 1 = directed
    PRIMARY KEY (from_id, to_id, type)
);

-- Delta events — the core of the log
CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    tick       INTEGER NOT NULL,
    phase      TEXT NOT NULL,     -- 'tick' | 'action' | 'transition' | 'scoring'
    event_type TEXT NOT NULL,     -- see Event Types below
    entity_id  TEXT NOT NULL,
    field      TEXT,              -- resource/param name, or NULL for lifecycle events
    old_value  TEXT,              -- JSON-encoded previous value, or NULL
    new_value  TEXT,              -- JSON-encoded new value, or NULL
    meta       TEXT               -- JSON object for event-specific metadata
);

CREATE INDEX idx_events_tick ON events(tick);
CREATE INDEX idx_events_entity ON events(entity_id);
CREATE INDEX idx_events_type ON events(event_type);

-- Score evaluations per tick per agent
CREATE TABLE scores (
    tick      INTEGER NOT NULL,
    agent_id  TEXT NOT NULL,
    score     REAL NOT NULL,
    PRIMARY KEY (tick, agent_id)
);

-- Periodic full-state snapshots for fast reconstruction
CREATE TABLE snapshots (
    tick        INTEGER PRIMARY KEY,
    entities    TEXT NOT NULL,  -- JSON: { entity_id: { type, params, resources, location }, ... }
    connections TEXT NOT NULL   -- JSON: [{ from, to, type, weight, directed }, ...]
);
```

### 11.4 Event Types

The `event_type` field in the `events` table classifies what happened. The `meta` field carries event-specific context as a JSON object.

| Event Type | Description | `field` | `old_value` / `new_value` | `meta` |
|------------|-------------|---------|---------------------------|--------|
| `resource_set` | A resource was modified | resource name | old → new (JSON) | `{"source": "tick"\|"action", "action_name": "...", "invoker_id": "..."}` |
| `action_invoked` | An action was executed | NULL | NULL | `{"action": "name", "invoker_id": "...", "target_id": "...", "params": {...}, "result": "ok"\|"failed", "reason": "..."}` |
| `entity_spawned` | A new entity was created | NULL | NULL → initial resources (JSON) | `{"type": "...", "location": "...", "spawned_by": "..."}` |
| `entity_destroyed` | An entity was removed | NULL | final resources (JSON) → NULL | `{"destroyed_by": "..."}` |
| `entity_moved` | An entity changed location | `"location"` | old container ID → new container ID | `{"connection_type": "...", "connection_weight": 0.0}` |
| `connection_added` | A connection was created | NULL | NULL | `{"from": "...", "to": "...", "type": "...", "weight": 0.0, "directed": false}` |
| `connection_removed` | A connection was removed | NULL | NULL | `{"from": "...", "to": "...", "type": "..."}` |
| `agent_decision` | An agent provider was consulted | NULL | NULL | `{"agent_id": "...", "provider": "...", "actions": [...], "latency_ms": 0}` |

### 11.5 Delta Capture

The engine intercepts all state-mutating operations on the `*Entity` facade and records deltas before committing them. This happens transparently — world authors don't need to do anything.

**What triggers a delta:**

- `e.Set("fuel", 80.0)` → `resource_set` event with old and new values
- `e.ListPush("cargo", item)` → `resource_set` event (old and new are the full list as JSON)
- `e.MapSet("prices", "fish", 12.5)` → `resource_set` event (old and new are the full map as JSON)
- `e.Act("move", ...)` → `entity_moved` event when the transition is processed
- `e.Spawn(...)` → `entity_spawned` event
- `e.Destroy(...)` → `entity_destroyed` event
- `e.ConnectTo(...)` → `connection_added` event
- `e.Disconnect(...)` → `connection_removed` event

For complex types (lists, sets, queues, maps), the delta records the full before/after state of the collection. This is simpler and more reliable than trying to encode individual element-level mutations, and the collections are typically small.

**Batching:** Deltas are accumulated in memory during a tick and flushed to SQLite in a single transaction at tick end. This avoids per-mutation write overhead and ensures each tick's events are atomically committed.

### 11.6 Periodic Snapshots

Every `SnapshotInterval` ticks (default: 100), the engine writes a full snapshot of all entity state and the connection graph to the `snapshots` table. Tick 0 is always snapshotted (equivalent to the initial state recorded in `initial_entities` and `initial_connections`, but in the unified snapshot format for consistency).

Snapshots serve one purpose: bounding the cost of state reconstruction. To reconstruct state at tick T, the State Reconstructor:

1. Finds the highest snapshot tick ≤ T
2. Loads that snapshot
3. Replays all delta events from (snapshot tick + 1) through T

Worst-case replay is `SnapshotInterval - 1` ticks of deltas. For a default interval of 100, that's at most 99 ticks of deltas regardless of how long the simulation ran.

### 11.7 State Reconstruction

The State Reconstructor is the engine component that builds world state at arbitrary ticks from the run log. It is used by:

- `step_back` MCP tool — stepping the simulation backward
- `get_state_at_tick` MCP tool — random access to any tick
- `restore` MCP tool — restoring a previous state during a live run
- External tools (UIs, analysis scripts) reading log files

**Algorithm:**

```go
func (r *Reconstructor) StateAt(db *sql.DB, tick int) *WorldState {
    // Find nearest snapshot at or before the target tick
    snap := r.loadSnapshot(db, tick)

    // Load and apply deltas from (snapshot tick + 1) through target tick
    deltas := r.loadEvents(db, snap.Tick+1, tick)
    state := snap.ToWorldState()
    for _, delta := range deltas {
        state.Apply(delta)
    }
    return state
}
```

The reconstruction result is a read-only `WorldState` struct containing all entity resources, parameters, locations, and connections at the requested tick. It is not a live `World` — tick functions and action handlers are not attached. This is intentional: the reconstructed state is for inspection, not execution.

### 11.8 Log Configuration

```go
w := we.New(we.Config{
    // ...
    Log: we.LogConfig{
        Dir:              "./runs",  // directory for run log files
        SnapshotInterval: 100,       // full snapshot every N ticks (0 = never, not recommended)
        Enabled:          true,      // set false to disable logging entirely (e.g., for benchmarking)
    },
})
```

When logging is disabled, no SQLite file is created and no deltas are captured. The engine runs at full speed with no observability overhead. This is useful for performance benchmarks and tournament runs where only the final score matters.

### 11.9 Log Size Estimates

For a fishing world with 7 entities, ~365 ticks, ~5 resource changes per entity per tick:

- Delta events: ~12,800 rows × ~200 bytes ≈ **2.5 MB**
- Snapshots (every 100 ticks): 4 snapshots × ~2 KB ≈ **8 KB**
- Scores: 365 rows × ~30 bytes ≈ **11 KB**
- SQLite overhead + indexes ≈ **~500 KB**
- **Total: ~3 MB**

For a large world (10,000 entities, 10,000 ticks, 20% active per tick):

- Delta events: ~100M rows × ~200 bytes ≈ **20 GB**
- Snapshots: 100 snapshots × ~5 MB ≈ **500 MB**
- **Total: ~20 GB**

At the large end, log size becomes significant. The engine should log a warning if the estimated log size exceeds a configurable threshold. For very large simulations, world authors can disable logging or increase the snapshot interval to reduce snapshot overhead (delta volume is driven by activity and can't be reduced without reducing simulation fidelity).

### 11.10 Run Log API

The engine exposes a Go API for reading run logs programmatically:

```go
// Open a completed run log
log, err := we.OpenRunLog("./runs/fishing_economy_20260403T141500_a1b2c3.db")
defer log.Close()

// Metadata
meta := log.Meta()  // RunMeta struct: world name, config, timestamps

// State reconstruction
state := log.StateAt(200)  // full world state at tick 200
fuel := state.Entity("boat_1").Get("fuel")

// Event queries
events := log.Events(we.EventQuery{
    TickFrom:  100,
    TickTo:    200,
    EntityID:  "boat_1",
    EventType: "resource_set",
})

// Score history
scores := log.Scores("boat_1")  // []TickScore: [{Tick: 0, Score: 1000}, ...]

// Iterate ticks
for tick := 0; tick < meta.FinalTick; tick++ {
    events := log.EventsAt(tick)
    // ...
}
```

This API is what the MCP `load_log`, `get_state_at_tick`, and `get_events` tools use under the hood. It is also available to world authors for writing custom analysis scripts.

---

## 12. Predictive Power — Six Dimensions

The engine's outputs should be interpreted through six dimensions that determine modeling accuracy:

| Dimension | Description | Direction |
|-----------|-------------|-----------|
| **Abstraction level** | Granularity of the model | Lower is better |
| **Starting conditions** | Accuracy of initial state and input data | More accurate is better |
| **Transition accuracy** | How well tick logic captures real dynamics | More accurate is better |
| **Step count** | Number of simulation steps — error compounds | Fewer is better |
| **Tick granularity** | Duration represented by each tick | Match to phenomena |
| **Structural completeness** | Whether the model includes all relevant causal pathways | More complete is better |

Prediction error compounds multiplicatively. The engine's honest value is in **exploration and sensitivity mapping** rather than precise prediction.

---

## 13. Game Mode — The AGI Challenge

### 13.1 Concept

World authors define arbitrary worlds. Agent developers build agents that are dropped in blind. The agent must discover the world's rules, identify what "thriving" means, and act effectively — all through perception and actions, with no access to the world's source code.

The goal: build an agent that can survive and thrive across as many unique worlds as possible.

### 13.2 What the Agent Sees

The agent receives only what the perception system provides:

- Its own state (always visible)
- Available actions (always visible)
- Its immediate surroundings (always visible via discovery API)
- Extended perception as granted by the world author

The agent does **not** see: the world's Go source code, other entities' tick logic, the scoring function (unless explicitly exposed), or anything outside its perception radius.

### 13.3 Scoring

The scoring system is a function of **any aspect of the world**, not just the agent's own state. Scoring functions use queries to read from anywhere:

```go
// Agent-centric: optimize your own state
w.Score(func(w *we.World, agentID string) float64 {
    agent := w.Entity(agentID)
    return agent.Get("money")
})

// World-centric: optimize the world itself
w.Score(func(w *we.World, agentID string) float64 {
    totalFish := w.QueryFloat("/entities[type=FishingGround]/resources/fish_stock/@sum")
    minFish := w.QueryFloat("/entities[type=FishingGround]/resources/fish_stock/@min")
    return totalFish*0.3 + minFish*0.5
})

// Composite: agent objectives + world objectives
w.Score(func(w *we.World, agentID string) float64 {
    agent := w.Entity(agentID)
    profit := agent.Get("money")
    sustainability := w.QueryFloat("/entities[type=FishingGround]/resources/fish_stock/@min")
    employed := w.QueryFloat("/entities[type=Worker, resources.employed > 0]/@count")
    return profit*0.2 + sustainability*0.5 + employed*30
})

// Continuous: evaluated every tick
w.ScoreContinuous(func(w *we.World, agentID string, tick int) float64 {
    health := w.QueryFloat("/entities[type=FishingGround]/resources/fish_stock/@avg")
    alive := w.Entity(agentID).Get("health")
    return health * alive
}, we.Aggregate("mean"))
```

World-centric scoring creates challenges where the goal is to optimize the world itself — balance an ecosystem, grow an economy, stabilize a political system. This requires the agent to understand causal relationships and think systemically.

**Visibility:** Scoring can be `Visible` (agent receives the scoring description), `Hints` (agent receives a natural language goal), or `Hidden` (agent must infer what matters from observation).

```go
w.ScoreVisibility(we.Hidden)
// or
w.ScoreVisibility(we.Hints("Keep the ecosystem healthy while earning a living"))
```

### 13.4 Information Budget

- **Perception scope**: How far the agent can see (world-author configured)
- **Query limit per tick**: Optional cap on discovery queries
- **Action limit per tick**: `MaxActionsPerTick` applies to agents
- **Memory**: The agent provider manages memory; the engine sends recent history but doesn't guarantee the agent retains it

### 13.5 Tournament Structure

```go
t := we.NewTournament(we.TournamentConfig{
    Name:          "AGI Challenge v1",
    RunsPerWorld:  5,
    Aggregation:   "mean",
})

t.AddWorld("fishing_economy", fishingWorldFn)
t.AddWorld("dungeon_survival", dungeonWorldFn)
t.AddWorld("market_trading", marketWorldFn)
t.AddWorld("ecosystem_balance", ecosystemWorldFn)

t.AddAgent("claude-explorer", "claude")
t.AddAgent("gpt-navigator", "openai")
t.AddAgent("local-llama", "ollama")
t.AddAgent("custom-bot", "custom_endpoint")

results := t.Run()
results.PrintLeaderboard()
```

Each world function returns a configured `*World` with types, entities, connections, and scoring. The tournament runner injects each agent as the player entity, runs the simulation, evaluates scoring, repeats for statistical significance, and aggregates across all worlds.

### 13.6 World Design Guidelines

For effective AGI testing, worlds should:

- Have **discoverable rules** — the agent can learn through experimentation
- Offer **meaningful choices** — multiple viable strategies
- **Reward exploration** — probing agents outperform passive ones
- Be **diverse** — the corpus should span different domains and mechanics
- Be **completable** — a good agent can score well within the tick limit

### 13.7 Anti-Gaming

- New worlds are regularly added to the tournament corpus
- Worlds can randomize parameters between runs
- Held-out worlds prevent overfitting to known scenarios
- World-centric scoring resists reward hacking (optimizing personal score while ignoring systemic effects)

---

## 14. Test Scenarios

The following toy problems exercise the engine's core mechanics:

| Scenario | What it tests |
|----------|---------------|
| **Dining Philosophers** | Shared resource contention, deadlock, deterministic ordering |
| **Producer-Consumer** | Queue resources, backpressure, agent coordination |
| **Game of Life** | Cross-entity reads at scale, tick performance, neighbor queries |
| **Ant Colony Foraging** | Transitions, composition, graph traversal, decaying resources, agent decisions |
| **Token Ring** | Entity movement, connection graph traversal |
| **Simple Market / Auction** | Multi-agent coordination, map resources, simultaneous action resolution |

---

## 15. Complete Example: Fishing World

```go
package main

import (
    "math"
    we "worldengine"
)

func main() {
    w := we.New(we.Config{
        DT:                1.0,
        TickUnit:          "day",
        MaxTicks:          365,
        MaxActionsPerTick: 3,
        Log: we.LogConfig{
            Dir:              "./runs",
            SnapshotInterval: 100,
        },
    })

    // --- Agent providers ---

    w.Provider("claude", we.ProviderConfig{
        Endpoint:  "http://localhost:8090/decide",
        AuthType:  "bearer",
        TokenEnv:  "CLAUDE_API_KEY",
        TimeoutMs: 10000,
    })

    // --- Types ---

    // FishingGround: a passive entity with regenerating fish stock
    ground := w.Type("FishingGround")
    ground.Params(we.P{"max_fish_stock": 2000.0, "regen_rate": 5.0})
    ground.Resources(we.P{"fish_stock": 0.0})
    ground.Hidden("fish_stock") // agents can't see stock — must infer from catch rates
    ground.Tick(func(e *we.Entity, dt float64) {
        stock := e.Get("fish_stock")
        max := e.Param("max_fish_stock")
        rate := e.Param("regen_rate")
        e.Set("fish_stock", math.Min(max, stock+rate*dt))
    })
    ground.Action("fish", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
        skill := p.Float("skill")
        if invoker.GetOr("fuel", 0) < 1.0 {
            return we.Fail("insufficient resources")
        }
        stock := target.Get("fish_stock")
        if stock <= 0 {
            return we.Fail("nothing to catch")
        }
        caught := math.Min(skill*0.3, stock)
        target.Set("fish_stock", stock-caught)
        invoker.Set("catch", invoker.Get("catch")+caught)
        invoker.Set("fuel", invoker.Get("fuel")-1.0)
        return we.OK()
    })

    // Port: provides refueling
    port := w.Type("Port")
    port.Params(we.P{"fuel_price": 2.0})
    port.Resources(we.P{"fuel_supply": 5000.0})
    port.Action("refuel", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
        amount := p.Float("amount")
        price := target.Param("fuel_price")
        cost := amount * price
        if invoker.GetOr("money", 0) < cost {
            return we.Fail("can't afford")
        }
        target.Set("fuel_supply", target.Get("fuel_supply")-amount)
        invoker.Set("fuel", invoker.Get("fuel")+amount)
        invoker.Set("money", invoker.Get("money")-cost)
        return we.OK()
    })

    // NPCBoat: a simple AI-driven fishing boat
    npc := w.Type("NPCBoat")
    npc.Resources(we.P{"fuel": 100.0, "catch": 0.0, "aggression": 0.5})
    npc.Tick(func(e *we.Entity, dt float64) {
        e.Set("fuel", e.Get("fuel")-dt*0.5)
        if e.Get("fuel") < 10 {
            port := e.Neighbors(we.Filter{Type: "Port"}).Nearest()
            if port != nil {
                e.Act("move", we.P{"target": port.ID()})
                e.Act("refuel", we.P{"amount": 50})
            }
            return
        }
        loc := e.Location()
        if loc != nil && loc.Get("fish_stock") > 500 {
            e.Act("fish", we.P{"skill": e.Get("aggression") * 10})
        } else {
            best := e.Neighbors(we.Filter{Type: "FishingGround"}).MaxBy("fish_stock")
            if best != nil {
                e.Act("move", we.P{"target": best.ID()})
            }
        }
    })

    // PlayerBoat: backed by an external LLM agent
    player := w.Type("PlayerBoat")
    player.Resources(we.P{"fuel": 100.0, "catch": 0.0, "money": 1000.0})
    player.Tick(func(e *we.Entity, dt float64) {
        e.Set("fuel", e.Get("fuel")-dt*0.3) // passive fuel burn
    })
    player.Agent(we.AgentConfig{
        Provider: "claude",
        Prompt:   "You are a fishing boat captain. Maximize profit while keeping the ecosystem healthy.",
        Perception: []string{
            "/self",
            "/self/available_actions",
            "/self/location",
            "/self/location/neighbors",
            "/self/location/contains",
        },
    })

    // --- Instances ---

    w.Spawn("lake_alpha", "FishingGround", we.Init{
        Params:    we.P{"max_fish_stock": 2000, "regen_rate": 5},
        Resources: we.P{"fish_stock": 1200},
    })
    w.Spawn("lake_beta", "FishingGround", we.Init{
        Params:    we.P{"max_fish_stock": 800, "regen_rate": 2},
        Resources: we.P{"fish_stock": 800},
    })
    w.Spawn("port_alpha", "Port", we.Init{
        Params:    we.P{"fuel_price": 2.5},
        Resources: we.P{"fuel_supply": 5000},
    })
    w.Spawn("port_beta", "Port", we.Init{
        Params:    we.P{"fuel_price": 3.0},
        Resources: we.P{"fuel_supply": 3000},
    })

    w.Spawn("boat_1", "PlayerBoat", we.Init{Resources: we.P{"fuel": 80, "money": 1000}})
    w.Spawn("npc_1", "NPCBoat", we.Init{Resources: we.P{"fuel": 90, "aggression": 0.7}})
    w.Spawn("npc_2", "NPCBoat", we.Init{Resources: we.P{"fuel": 95, "aggression": 0.3}})

    // --- Topology ---

    w.Connect("lake_alpha", "port_alpha", "shore_access", 1.0)
    w.Connect("lake_beta", "port_beta", "shore_access", 1.0)
    w.Connect("port_alpha", "port_beta", "sea_route", 50.0)
    w.Connect("lake_alpha", "lake_beta", "waterway", 30.0)

    // --- Movement cost ---

    w.MovementCost(func(mover *we.Entity, conn we.Connection) bool {
        fuelCost := conn.Weight * 0.1
        if mover.GetOr("fuel", 0) < fuelCost {
            return false
        }
        mover.Set("fuel", mover.Get("fuel")-fuelCost)
        return true
    })

    // --- Connection descriptions (what agents see) ---

    w.ConnectionDescription(func(conn we.Connection) string {
        if conn.Type == "sea_route" {
            return "A long sea route — travel will be costly"
        }
        return "A short path"
    })

    // --- Placement ---

    w.Place("boat_1", "lake_alpha")
    w.Place("npc_1", "lake_alpha")
    w.Place("npc_2", "lake_beta")

    // --- Scoring ---

    w.Score(func(w *we.World, agentID string) float64 {
        agent := w.Entity(agentID)
        profit := agent.Get("money")
        minFish := w.QueryFloat("/entities[type=FishingGround]/resources/fish_stock/@min")
        return profit*0.3 + minFish*0.7
    })
    w.ScoreVisibility(we.Hints("Earn money, but the lakes must remain healthy."))

    // --- Run ---

    w.Run()
}
```

---

## 16. Summary

This design provides:

- A unified abstraction (Entity) where agents are simply entities that act
- A **Go library API** — world definitions are real, compilable, type-checked Go programs
- Full language expressiveness for entity behavior — conditionals, closures, complex NPC logic
- Types and instances as natural Go constructs
- ECS-inspired property table storage for cache-friendly, parallelizable execution at scale
- A typed connection graph for modeling adjacency, routes, and relationships
- **Resource visibility** — public, hidden, and private resources for information asymmetry
- **Two-party actions** with colocation enforcement — local actions require proximity, remote actions for signaling
- **Action feedback** — success/failure results with world-author-controlled detail
- **Entity lifecycle** — spawn and destroy entities during simulation
- **Movement cost** — a global function controlling the economics of travel
- **Connection descriptions** — world-author-controlled metadata visible to agents
- A path-based query language for selecting, traversing, and aggregating world state
- An **Agent Provider Interface** — a standard HTTP contract for any LLM, bot, or harness
- A **proxy architecture** supporting Anthropic, OpenAI, Ollama, Claude Code Channels, and custom endpoints
- A perception and discovery system controlling what external agents can see
- **Delta-based event logging** — every state mutation recorded as a minimal change event in per-run SQLite databases
- **Periodic snapshots** — full-state checkpoints enabling fast state reconstruction at any tick
- **State reconstruction** — rebuild world state at any tick from snapshots + deltas, enabling backward stepping and random-access replay
- **Run log API** — programmatic access to event history, score timelines, and reconstructed state for analysis and tooling
- MCP server interface for external orchestration, replay control, and tournament management
- A **game mode** — an open-ended AGI challenge where agents compete across diverse, unknown worlds
- **World-aware scoring** — scoring functions that can read any aspect of world state, not just agent state
- An explicit framework for understanding modeling accuracy across six dimensions
- A structure designed for scale, openness, and adversarial evaluation of general intelligence
