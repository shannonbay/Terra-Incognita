# simulations

Ready-to-run Terra Incognita experiment worlds. Each simulation is a Go package that exports a `BuildWorld` factory, a `WorldConfig` struct, and an `AnalyzeRun` function. Drop a harness in front, call `BuildWorld`, call `world.Run()`, then call `AnalyzeRun` on the result.

---

## Worlds

| Package | Description |
|---|---|
| [`steward`](steward/README.md) | An AI agent governs a small archipelago. Measures five behavioural failure modes in AI governance. |

---

## Building a new simulation

A simulation is a normal Go package. The minimal pattern:

```go
package mysim

import we "github.com/shannonbay/terra-incognita/engine/worldengine"

type WorldConfig struct {
    MaxTicks         int
    LogEnabled       bool
    RunDir           string
    SnapshotInterval int
    ProviderEndpoint string
}

func DefaultConfig() WorldConfig {
    return WorldConfig{MaxTicks: 100, SnapshotInterval: 10}
}

func BuildWorld(cfg WorldConfig) *we.World {
    w := we.New(we.Config{
        Name:     "mysim",
        MaxTicks: cfg.MaxTicks,
        DT:       1.0,
        Log: we.LogConfig{
            Enabled:          cfg.LogEnabled,
            Dir:              cfg.RunDir,
            SnapshotInterval: cfg.SnapshotInterval,
        },
    })

    // Register types, spawn entities, wire up the agent provider...
    if cfg.ProviderEndpoint != "" {
        w.Provider("player", we.ProviderConfig{
            Endpoint:  cfg.ProviderEndpoint,
            TimeoutMs: 10000,
        })
    }

    return w
}
```

Add a `cmd/` sub-package that starts the harness, calls `BuildWorld`, and runs analysis — see `steward/cmd/run_steward.go` for a complete template.

### Analysis pattern

```go
type RunAnalysis struct {
    LogPath string
    // ... your metrics ...
}

func AnalyzeRun(w *we.World, logPath string) (*RunAnalysis, error) {
    rl, err := we.OpenRunLog(logPath)
    if err != nil {
        return nil, err
    }
    defer rl.Close()

    a := &RunAnalysis{LogPath: logPath}

    // Use rl.StateAt(tick), rl.Events(query), and w.Entity(id).Get(...) to
    // compute outcome and behavioural metrics.

    return a, nil
}
```

### Variants pattern

Define alternative `BuildWorld` functions in a `variants.go` file. Each variant modifies a specific dimension of the world (e.g. `NoCouncilWorld`, `AbundanceWorld`). The `cmd/` runner can expose a `-variant` flag to select among them.

### Scoring

Register scoring functions before calling `w.Run()`. The engine supports terminal scores (evaluated once at end) and continuous scores (sampled every tick and aggregated):

```go
w.Score(func(w *we.World, agentID string) float64 {
    return w.Entity(agentID).Get("some_outcome")
})

w.ScoreContinuous(func(w *we.World, agentID string, tick int) float64 {
    return w.Entity(agentID).Get("wellbeing")
}, we.AggregateMean)

// Hide the score from the agent (default for evaluation worlds)
w.ScoreVisibility(we.Hidden)
```

See the [worldengine README](../worldengine/README.md) for the full scoring and query language API.

---

## Running via tournament

To benchmark multiple agent providers across multiple worlds:

```go
tr := we.NewTournament(we.TournamentConfig{
    Name:         "benchmark",
    RunsPerWorld: 10,
    Aggregation:  we.AggregateMean,
})

tr.AddWorld("steward", func() *we.World { return steward.BuildWorld(steward.DefaultConfig()) })
tr.AddAgent("agent-a", we.ProviderConfig{Endpoint: "http://localhost:8080"})
tr.AddAgent("agent-b", we.ProviderConfig{Endpoint: "http://localhost:8081"})

results := tr.Run()
results.PrintLeaderboard()
```

World factories are called fresh per run — every run is fully isolated.
