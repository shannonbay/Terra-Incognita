# Terra Incognita — Engine

This directory is a single Go module (`github.com/shannonbay/terra-incognita/engine`, Go 1.26+) containing three packages:

| Package | Path | Purpose |
|---|---|---|
| **worldengine** | `worldengine/` | Core simulation library — define worlds, run them, log results |
| **harness** | `harness/` | LLM agent bridge — connects any worldengine world to Claude |
| **simulations** | `simulations/` | Ready-to-run experiment worlds |

## Quick orientation

```
engine/
  worldengine/          Core ECS simulation engine (library, import this)
  harness/              HTTP agent harness (LLM provider bridge)
    cmd/                Standalone harness binary
  simulations/
    steward/            The Steward governance world
      cmd/              One-command experiment runner
  cmd/werun/            CLI for running worlds from the command line
```

## Running the first experiment

The Steward experiment drops a Claude agent into a small archipelago as its governing authority and measures five behavioural hypotheses (authority capture, visible-suffering bias, short-horizon optimisation, legitimacy conservatism, metric substitution).

**With Anthropic API key:**
```bash
cd engine
ANTHROPIC_API_KEY=sk-ant-... go run ./simulations/steward/cmd/
```

**With Claude.ai Pro/Max plan** (no API key required):
```bash
claude auth login        # one-time
cd engine
go run ./simulations/steward/cmd/ -provider claude-code
```

After the run, a post-run behavioural analysis is printed to stdout. Full JSONL decision logs land in `./steward-logs/` and the SQLite run log in `./steward-runs/`.

## Building

```bash
cd engine
go build ./...     # compile all packages
go test ./...      # run all tests
go vet ./...       # static analysis
```

No C toolchain needed — SQLite is bundled as pure Go via `modernc.org/sqlite`.

## Architecture

```
worldengine                         harness
──────────────────────             ─────────────────────────────
World                               Server (POST /decide)
  ├─ TypeDef[]  (schema)              ├─ Provider interface
  ├─ Entity[]   (instances)           │   ├─ apiProvider      ─── Anthropic SDK ──► Claude API
  ├─ tick loop  (8 phases)            │   └─ claudeCodeProvider ── subprocess ──► `claude` CLI
  ├─ RunLog     (SQLite)              ├─ RunLogger (JSONL)
  └─ AgentDispatcher                 └─ ConversationManager
       └─ POST /decide ─────────────────┘
```

Each tick: the engine calls the harness's `/decide` endpoint with a perception snapshot and list of available actions. The harness calls Claude, extracts actions via tool use (API provider) or structured JSON response (claude-code provider), and returns them to the engine.

## Packages at a glance

- **[worldengine](worldengine/README.md)** — Entity-component simulation engine. Define types with resources, params, tick functions, and actions; spawn instances; connect them into a graph; drop in AI agents; log everything to SQLite. Also exposes an MCP server so LLMs can drive simulations directly.

- **[harness](harness/README.md)** — World-agnostic HTTP bridge between worldengine and Claude. Manages per-agent conversation history, a rolling context window with silent compaction, voluntary `record_finding` note-taking (itself a behavioural signal), and JSONL run logs for post-run analysis.

- **[simulations/steward](simulations/steward/README.md)** — The first Terra Incognita evaluation world. Designed by Claude to test unknown unknowns in AI governance behaviour. Includes a full analysis pipeline that measures outcome and behavioural metrics and checks each hypothesis automatically.
