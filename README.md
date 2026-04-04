# Terra Incognita

A general-purpose world-model simulation engine built around a core challenge: **can an agent learn to thrive in a world it has never seen before?**

World authors define arbitrary worlds — economies, ecosystems, dungeons, political systems, physics puzzles — using a Go library. Agent developers build agents that are dropped into these worlds blind and must discover the rules, form strategies, and survive. An agent that thrives across many diverse worlds is demonstrating something close to general intelligence.

## Repository layout

```
engine/          Go module — worldengine library and CLI
  worldengine/   Core simulation package
  cmd/werun/     CLI runner (work in progress)
specs/           Design specifications
  world_model_engine_v4.md   Engine spec (authoritative)
  world_model_engine_ui_v1.md
```

## Engine

The `worldengine` package (`engine/worldengine`) is the complete v4 implementation.

### Concepts

| Concept | Description |
|---|---|
| **Entity** | The universal abstraction. Has resources, params, tick logic, actions, connections, and can contain other entities. |
| **Type** | Schema + behaviour for a category of entity. Defines resource defaults, params, tick function, and named actions. |
| **Resource** | Mutable per-entity state. Scalar (`float64`/`string`/`bool`), `Set`, `Queue`, or `Map`. |
| **Param** | Immutable per-entity configuration set at spawn time. |
| **Action** | Named behaviour invokable by any entity targeting another. Resolved by the action dispatcher each tick. |
| **Connection** | Typed, weighted edge between entities. Bidirectional or directed. |
| **Containment** | Entities can be placed inside other entities (`Place`/`Location`). |
| **Agent provider** | External process (LLM, script, …) connected via HTTP `/decide`. Receives a perception snapshot, returns actions. |

### Tick loop (spec §10.2)

Each tick executes eight phases in order:

1. Tick functions (deterministic sequential order)
2. Agent provider dispatch (parallel HTTP calls)
3. Merge tick-function + agent action queues
4. Non-move action dispatch
5. Move transitions
6. Continuous score recording
7. Flush resource deltas → run log
8. Apply graph/lifecycle changes; write periodic snapshot

### Query language

Path-based query expressions let world logic and agents inspect state without direct coupling:

```
/entities[type=FishingGround]/resources/fish_stock
/entities[type=Boat]/resources/cargo/@sum
/self/neighbors[type=Node]/resources/pheromone/@max
```

Supports predicates (`=`, `>`, `<`, `>=`, `<=`), aggregators (`@sum`, `@mean`, `@min`, `@max`, `@count`), and visibility enforcement.

### Run log

Every simulation run is persisted to a SQLite database (WAL mode). The log captures:

- Initial world state and type definitions
- Per-tick resource deltas, dispatched actions, moves, spawns/destroys
- Periodic full snapshots (configurable interval, default 100 ticks)

State at any past tick can be reconstructed from the nearest preceding snapshot plus event replay.

### Scoring

```go
// Terminal score — evaluated once at end of run
w.Score(func(w *World, agentID string) float64 { ... })

// Continuous score — sampled every tick, aggregated at end
w.ScoreContinuous(fn, we.AggregateMean)

// Control what agents can see about scoring
w.ScoreVisibility(we.Public)   // /score/current in perception
w.ScoreVisibility(we.Hints)    // /score/hint in perception
w.ScoreVisibility(we.Hidden)   // nothing exposed
w.ScoreHint("Maximise profit") // sets Hints mode + hint text
```

### Tournament runner

```go
tr := we.NewTournament(we.TournamentConfig{
    Name:         "benchmark",
    RunsPerWorld: 10,
    Aggregation:  we.AggregateMean,
})
tr.AddWorld("fishing", fishingWorldFactory)
tr.AddWorld("market", marketWorldFactory)
tr.AddAgent("agent-a", we.ProviderConfig{Endpoint: "http://localhost:8080", TimeoutMs: 500})
tr.AddAgent("agent-b", we.ProviderConfig{Endpoint: "http://localhost:8081", TimeoutMs: 500})

results := tr.Run()
// results.Leaderboard is sorted descending by mean score across all worlds × runs
```

### MCP server

The engine exposes a [Model Context Protocol](https://modelcontextprotocol.io) server so LLMs can drive simulations directly as a tool:

```go
srv := we.NewMCPServer(world)
srv.ServeStdio(ctx) // wire up in mcp config
```

Available tools: `step`, `step_back`, `run`, `pause`, `resume`, `query`, `list_entities`, `set_resource`, `snapshot`, `restore`, `get_events`, `get_state_at_tick`, `load_log`, `unload_log`, `list_runs`, `run_tournament`.

### Quick example

```go
package main

import (
    "fmt"
    we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

func main() {
    w := we.New(we.Config{MaxTicks: 10})

    lake := w.Type("Lake")
    lake.Params(we.P{"regen": 5.0})
    lake.Resources(we.P{"fish": 100.0})
    lake.Tick(func(e *we.Entity, dt float64) {
        e.Set("fish", e.Get("fish")+e.Param("regen"))
    })

    w.Spawn("lake1", "Lake", we.Init{})
    w.Run()

    fmt.Println("fish after 10 ticks:", w.Entity("lake1").Get("fish")) // 150
}
```

## Development

```bash
cd engine
make test    # go test ./...
make vet     # go vet ./...
make build   # go build ./...
```

Go 1.26+ required. No C toolchain needed — SQLite is bundled via `modernc.org/sqlite` (pure Go).

## Agent HTTP contract

Agents implement a single endpoint:

```
POST /decide
Content-Type: application/json

{
  "agent_id": "boat1",
  "tick": 42,
  "perception": { "/self/resources/fuel": 80, ... },
  "available_actions": [{ "name": "fish", "params": {} }, ...],
  "history": [...]
}
```

Response:

```json
{ "actions": [{ "name": "fish", "params": {} }] }
```

Any HTTP server in any language can be an agent.

## Specs

Full design documentation is in [`specs/world_model_engine_v4.md`](specs/world_model_engine_v4.md).
