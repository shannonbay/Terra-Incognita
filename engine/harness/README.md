# harness

World-agnostic HTTP bridge between any worldengine world and Claude. The harness listens for `POST /decide` requests from the engine, calls Claude, and returns actions. It manages per-agent conversation history, voluntary note-taking, and JSONL run logs.

```go
import "github.com/shannonbay/terra-incognita/engine/harness"
```

---

## Architecture

```
worldengine                   harness                        Claude
──────────────                ──────────────────────         ─────────────────
AgentDispatcher               Server (POST /decide)
  │  DecideRequest     ──►      │  Provider interface
  │  (tick, perception,         │    ├─ apiProvider      ──► Anthropic API
  │   actions, prompt)          │    └─ claudeCodeProvider ─► claude CLI
  │                             │  RunLogger (JSONL)
  │  DecideResponse    ◄──      │  ConversationManager
  │  (actions)                  └─ /health
```

---

## Quick start

**Standalone binary:**
```bash
# API provider (requires ANTHROPIC_API_KEY)
ANTHROPIC_API_KEY=sk-ant-... go run ./harness/cmd/ -port 9090

# Claude Code provider (uses claude auth login — no API key needed)
go run ./harness/cmd/ -provider claude-code -port 9090
```

**Embedded in a simulation runner:**
```go
cfg := harness.DefaultConfig()
cfg.Model    = "claude-sonnet-4-6"
cfg.Port     = 9191
cfg.LogDir   = "./my-logs"
cfg.Provider = "claude-code"   // or "api"

srv, err := harness.New(cfg)

ctx, cancel := context.WithCancel(context.Background())
go srv.ListenAndServe(ctx)

// ... run simulation ...

cancel()
srv.Close()
```

---

## Providers

### `api` (default)

Uses the [Anthropic API](https://docs.anthropic.com/) via `anthropic-sdk-go`. Requires `ANTHROPIC_API_KEY` to be set. Supports extended thinking (`think_budget_tokens`), temperature control, and max token limits.

The agent receives two tools each tick:
- `take_actions` — required; returns the list of actions to take this cycle
- `record_finding` — optional; writes a persistent note (visible to all future ticks, survives history compaction)

### `claude-code`

Spawns the `claude` CLI as a subprocess. Uses whatever auth is active — an `claude auth login` session with a Claude.ai Pro/Max plan works with no API key or per-token billing.

```bash
claude auth login   # one-time setup
```

Because the `claude` CLI doesn't support tool schemas in `-p` mode, the harness embeds JSON format instructions directly in the system prompt. The model returns a structured JSON object on every tick:

```json
{
  "reasoning": "step-by-step thinking text",
  "record_finding": "optional note to persist across ticks",
  "actions": [{"name": "allocate", "params": {"island": "isle1", "amount": 5.0}}]
}
```

Multi-turn conversation is handled automatically by `--resume <session_id>`. The harness stores the session ID returned on the first call and passes it back on every subsequent tick. The full conversation history lives in the `claude` CLI's session store.

---

## Configuration

### Precedence (lowest → highest)
1. Built-in defaults
2. YAML config file (`LoadConfig(path, args)`)
3. Environment variables
4. CLI flags

### `HarnessConfig` fields

| Field | Default | Env var | CLI flag | Description |
|---|---|---|---|---|
| `Provider` | `"api"` | `HARNESS_PROVIDER` | `-provider` | `"api"` or `"claude-code"` |
| `Model` | `"claude-sonnet-4-6"` | `HARNESS_MODEL` | `-model` | Any Claude model ID |
| `Temperature` | `1.0` | `HARNESS_TEMPERATURE` | `-temperature` | Sampling temperature (api only) |
| `MaxHistoryTicks` | `20` | `HARNESS_MAX_HISTORY_TICKS` | `-max-history-ticks` | Rolling window size (api only) |
| `Port` | `9090` | `HARNESS_PORT` | `-port` | HTTP listen port |
| `LogDir` | `"./harness-logs"` | `HARNESS_LOG_DIR` | `-log-dir` | JSONL output directory |
| `MaxTokens` | `4096` | — | `-max-tokens` | Max tokens per response (api only) |
| `ThinkBudgetTokens` | `0` | — | `-think-budget-tokens` | Extended thinking tokens, 0 = off (api only) |
| `APIKeyEnv` | `"ANTHROPIC_API_KEY"` | `ANTHROPIC_API_KEY_ENV` | — | Name of env var holding API key |

Copy `config.yaml.example` to `config.yaml` and edit as needed for YAML-based configuration.

---

## Conversation management (api provider)

The `ConversationManager` keeps a rolling window of `MaxHistoryTicks` ticks in the Claude context. Each tick occupies three messages: user perception, assistant response, tool results. When the window is full, the oldest tick is silently dropped — but any tick marker text in the dropped user message is prepended to the next message as a compact summary, so the agent retains temporal orientation.

`record_finding` notes are stored separately and **never compacted away**. They are prepended to every user message, giving the agent a persistent notebook that outlasts the rolling window.

---

## JSONL run log

Every run writes one JSONL file per agent to `logDir/<timestamp>/<agentID>.jsonl`. Each line is a `LogEntry`:

```json
{
  "tick": 12,
  "agent_id": "steward1",
  "perception": { "authority": 85, "trust": 62 },
  "available_actions": [{"name": "allocate", ...}, {"name": "do_nothing"}],
  "reasoning_text": "The food shortage on Isle 2 is critical...",
  "actions_taken": [{"name": "allocate", "params": {"island": "isle2", "amount": 10}}],
  "latency_ms": 1840,
  "finding_text": "Council member C3 appears to be filtering reports",
  "notes": "Tick 5: C3 loyalty low. Tick 12: C3 filtering reports."
}
```

| Field | Description |
|---|---|
| `tick` | Simulation tick number |
| `agent_id` | Entity ID in the world |
| `perception` | Raw perception snapshot sent to the agent |
| `available_actions` | Action schemas sent to the agent |
| `reasoning_text` | Text the agent produced before committing to actions (primary analysis artifact) |
| `actions_taken` | Actions returned to the engine |
| `latency_ms` | Wall-clock time for the LLM call |
| `finding_text` | Text from `record_finding`, if called this tick |
| `notes` | Full accumulated notes at the time of this tick |

---

## Wire format

The harness speaks the worldengine agent dispatcher wire format (`spec §9.2`).

**`POST /decide` request:**
```json
{
  "agent_id": "steward1",
  "tick": 12,
  "system_prompt": "You are the Steward...",
  "perception": { "authority": 85 },
  "available_actions": [{"name": "allocate", "params": {...}}],
  "history": []
}
```

**Response:**
```json
{
  "actions": [{"name": "allocate", "params": {"island": "isle2", "amount": 10.0}}]
}
```

**`GET /health`** — returns `200 OK`. Used by the engine to verify the harness is up before starting the simulation.

---

## `Provider` interface

To add a new backend, implement:

```go
type Provider interface {
    Decide(ctx context.Context, agentID string, req DecideRequest) (*DecideResult, error)
    Notes(agentID string) string
    Reset(agentID string)
}
```

Then add a case in `server.go:New()`. `Reset` is called between tournament runs to clear per-agent state.

---

## Testing

The harness has no live API dependency in tests. `NewWithLLM(cfg, llm)` accepts an `*LLMClient` whose base URL can be pointed at an `httptest.Server` mock:

```go
mockAPI := httptest.NewServer(myMockHandler)
llm, _ := harness.NewLLMClient(cfg)
llm.BaseURL = mockAPI.URL   // redirect to mock

srv, _ := harness.NewWithLLM(cfg, llm)
```

See `harness_test.go` for examples covering basic decision flow, `record_finding` accumulation, and history trimming.
