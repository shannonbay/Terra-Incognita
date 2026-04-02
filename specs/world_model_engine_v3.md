# World Model Engine — Design Specification v3

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

ground.Tick(func(e *we.Entity, dt float64) {
    stock := e.Get("fish_stock")
    max := e.Param("max_fish_stock")
    rate := e.Param("regen_rate")
    e.Set("fish_stock", math.Min(max, stock+rate*dt))
})

ground.Action("fish", func(e *we.Entity, p we.P) {
    skill := p.Float("skill")
    e.Set("fish_stock", math.Max(0, e.Get("fish_stock")-skill*0.5))
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
e.Get("fuel") float64             // get a resource value
e.Set("fuel", 80.0)               // set a resource value
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

### 4.2 Property Table Storage

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

### 5.3 Mutable Connections

The connection graph is mutable during simulation:

```go
// Inside a tick function
e.ConnectTo("new_ally", "alliance", 1.0)
e.Disconnect("former_ally", "alliance")
```

---

## 6. Actions

### 6.1 Definition

Actions are registered on a type:

```go
port := w.Type("Port")

port.Action("refuel", func(e *we.Entity, p we.P) {
    amount := p.Float("amount")
    price := e.Param("fuel_price")
    e.Set("fuel_supply", e.Get("fuel_supply")-amount)
    // The invoking entity pays — cross-entity interaction
    // handled via the action dispatcher (see §6.3)
})
```

### 6.2 Invocation

Tick functions invoke actions via `e.Act()`:

```go
e.Act("fish", we.P{"skill": 5})                          // act on self/location
e.Act("refuel", we.P{"amount": 50, "target": "port_alpha"}) // act on a specific target
```

### 6.3 Cross-Entity Actions

When an action includes a `"target"` param, the engine resolves the target entity and executes the action handler against the target's state. The invoking entity's ID is available to the action handler via `p.String("_invoker")`, enabling interactions where both entities are affected (e.g., deducting money from the invoker while adding fuel).

### 6.4 Actions Per Tick

A tick function may invoke zero or more actions. Actions are collected and executed sequentially after all tick functions complete. The `MaxActionsPerTick` config parameter enforces a limit when needed.

---

## 7. Tick Model

### 7.1 Tick Loop

Each tick:

1. Execute tick logic for all entities (collects invoked actions)
2. Dispatch agent provider calls for agent-backed entities (collects actions)
3. Merge and order all actions deterministically
4. Execute actions sequentially
5. Process transitions (entity movement)
6. Commit state changes
7. Log events

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
    { "tick": 41, "actions": [{ "name": "fish", "params": { "skill": 5 } }], "result": "ok" }
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
| `/self/location` | The entity the agent is inside |
| `/self/location/entities` | All entities at the agent's current location |
| `/self/connections` | The agent's direct connections |

Broader perception (neighbors, type filtering, deeper traversal) must be granted in the perception config.

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
| `run` | Run until a condition is met or max ticks reached |
| `query` | Execute a query language expression |
| `snapshot` | Save the current world state |
| `restore` | Restore a previous snapshot |
| `set_resource` | Modify an entity's resource |
| `list_entities` | List entities with optional type filter |
| `get_events` | Retrieve the event log for the last N ticks |
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
| **Snapshot Manager** | Save and restore world state |
| **Event Log** | Record actions and state changes for observability |
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

    // Phase 4: Execute actions sequentially
    for _, action := range allActions {
        dispatcher.Execute(tables, action)
    }

    // Phase 5: Process transitions
    transitions.Process(tables, connectionGraph)

    // Phase 6: Log events
    eventLog.Record(tick, allActions)
}
```

### 10.3 No Code Generation

Unlike v2 of this spec, there is no code generation pipeline. World definitions compile as normal Go programs. The library registers tick functions and action handlers at init time; the engine calls them directly. This simplifies the toolchain to: write Go → compile → run.

---

## 11. Predictive Power — Six Dimensions

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

## 12. Game Mode — The AGI Challenge

### 12.1 Concept

World authors define arbitrary worlds. Agent developers build agents that are dropped in blind. The agent must discover the world's rules, identify what "thriving" means, and act effectively — all through perception and actions, with no access to the world's source code.

The goal: build an agent that can survive and thrive across as many unique worlds as possible.

### 12.2 What the Agent Sees

The agent receives only what the perception system provides:

- Its own state (always visible)
- Available actions (always visible)
- Its immediate surroundings (always visible via discovery API)
- Extended perception as granted by the world author

The agent does **not** see: the world's Go source code, other entities' tick logic, the scoring function (unless explicitly exposed), or anything outside its perception radius.

### 12.3 Scoring

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

### 12.4 Information Budget

- **Perception scope**: How far the agent can see (world-author configured)
- **Query limit per tick**: Optional cap on discovery queries
- **Action limit per tick**: `MaxActionsPerTick` applies to agents
- **Memory**: The agent provider manages memory; the engine sends recent history but doesn't guarantee the agent retains it

### 12.5 Tournament Structure

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

### 12.6 World Design Guidelines

For effective AGI testing, worlds should:

- Have **discoverable rules** — the agent can learn through experimentation
- Offer **meaningful choices** — multiple viable strategies
- **Reward exploration** — probing agents outperform passive ones
- Be **diverse** — the corpus should span different domains and mechanics
- Be **completable** — a good agent can score well within the tick limit

### 12.7 Anti-Gaming

- New worlds are regularly added to the tournament corpus
- Worlds can randomize parameters between runs
- Held-out worlds prevent overfitting to known scenarios
- World-centric scoring resists reward hacking (optimizing personal score while ignoring systemic effects)

---

## 13. Test Scenarios

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

## 14. Complete Example: Fishing World

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
    ground.Tick(func(e *we.Entity, dt float64) {
        stock := e.Get("fish_stock")
        max := e.Param("max_fish_stock")
        rate := e.Param("regen_rate")
        e.Set("fish_stock", math.Min(max, stock+rate*dt))
    })
    ground.Action("fish", func(e *we.Entity, p we.P) {
        skill := p.Float("skill")
        e.Set("fish_stock", math.Max(0, e.Get("fish_stock")-skill*0.5))
    })

    // Port: provides refueling
    port := w.Type("Port")
    port.Params(we.P{"fuel_price": 2.0})
    port.Resources(we.P{"fuel_supply": 5000.0})
    port.Action("refuel", func(e *we.Entity, p we.P) {
        amount := p.Float("amount")
        e.Set("fuel_supply", e.Get("fuel_supply")-amount)
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

## 15. Summary

This design provides:

- A unified abstraction (Entity) where agents are simply entities that act
- A **Go library API** — world definitions are real, compilable, type-checked Go programs
- Full language expressiveness for entity behavior — conditionals, closures, complex NPC logic
- Types and instances as natural Go constructs
- ECS-inspired property table storage for cache-friendly, parallelizable execution at scale
- A typed connection graph for modeling adjacency, routes, and relationships
- A path-based query language for selecting, traversing, and aggregating world state
- An **Agent Provider Interface** — a standard HTTP contract for any LLM, bot, or harness
- A **proxy architecture** supporting Anthropic, OpenAI, Ollama, Claude Code Channels, and custom endpoints
- A perception and discovery system controlling what external agents can see
- MCP server interface for external orchestration and tournament management
- A **game mode** — an open-ended AGI challenge where agents compete across diverse, unknown worlds
- **World-aware scoring** — scoring functions that can read any aspect of world state, not just agent state
- An explicit framework for understanding modeling accuracy across six dimensions
- A structure designed for scale, openness, and adversarial evaluation of general intelligence
