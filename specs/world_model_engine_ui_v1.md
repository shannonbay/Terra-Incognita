# World Model Engine — UI & Control API Specification v1

## 1. Introduction

This specification covers the browser-based UI for the World Model Engine and the HTTP control API that connects it to running simulations and completed run logs. It is a companion to the Engine Design Specification (v4), which defines the engine library, event log schema, and MCP server interface.

The UI serves two purposes: **live control** of running simulations (step, pause, resume, inspect) and **replay** of completed runs from log files. Both modes present the same interface — an entity tree, detail pane, event feed, connection graph, timeline, and score chart. The difference is only in what controls are available: live mode can modify state and advance the simulation; replay mode can only scrub through history.

### 1.1 Design Targets

- **Two modes, one interface**: Live and replay share the same layout and components; mode determines which controls are enabled
- **Responsive scrubbing**: State at any tick reconstructable in under 100ms for typical worlds (leveraging the snapshot + delta architecture from the engine spec)
- **Zero engine modification**: The UI communicates through the engine's existing HTTP/MCP interface — no changes to the engine library required
- **Standalone deployment**: The UI is a single static web application served by the control server; no build step required to view a run log
- **Scale-appropriate**: Usable with worlds up to 10,000 entities and 10,000 ticks, with graceful degradation beyond that

---

## 2. Architecture

### 2.1 Component Overview

```
┌──────────────────────────────────────────────────────────┐
│                     Browser (UI)                         │
│  ┌────────────┬─────────────┬──────────┬───────────────┐ │
│  │ Entity Tree│ Detail Pane │ Graph    │ Timeline/Score│ │
│  └────────────┴─────────────┴──────────┴───────────────┘ │
│                         │                                │
│                    WebSocket + REST                       │
└─────────────────────────┬────────────────────────────────┘
                          │
┌─────────────────────────▼────────────────────────────────┐
│                   Control Server                         │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────┐ │
│  │ REST API     │  │ WebSocket    │  │ State Cache    │ │
│  │ (query,      │  │ (live events,│  │ (reconstructed │ │
│  │  control)    │  │  tick push)  │  │  states, LRU)  │ │
│  └──────┬───────┘  └──────┬───────┘  └────────────────┘ │
│         │                 │                              │
│  ┌──────▼─────────────────▼──────────────────┐           │
│  │         Session Manager                    │           │
│  │  ┌─────────────┐  ┌────────────────────┐  │           │
│  │  │ Live Session │  │ Replay Session     │  │           │
│  │  │ (engine MCP) │  │ (run log .db file) │  │           │
│  │  └─────────────┘  └────────────────────┘  │           │
│  └────────────────────────────────────────────┘           │
└──────────────────────────────────────────────────────────┘
```

### 2.2 Control Server

The control server is a Go binary (`weui`) that serves the static UI assets and provides the HTTP + WebSocket API. It manages two types of sessions:

**Live sessions** connect to a running engine process via its MCP interface. The control server proxies UI requests to the engine's MCP tools (`step`, `pause`, `resume`, `query`, `get_state_at_tick`, etc.) and subscribes to the engine's event stream for real-time updates.

**Replay sessions** open a completed run log (`.db` file) directly. The control server reads the SQLite database using the engine's `RunLog` API, reconstructs state at requested ticks, and serves events and scores from the log. No running engine process is needed.

The control server maintains an LRU cache of recently reconstructed states to avoid redundant snapshot + delta replay when the UI scrubs back and forth over the same region.

### 2.3 Startup

```bash
# Replay mode — open a completed run log
weui replay ./runs/fishing_economy_20260403T141500_a1b2c3.db

# Live mode — connect to a running engine
weui live localhost:9090

# Browse mode — file picker for available run logs
weui browse ./runs/
```

All modes start a local HTTP server (default port 8080) and open the browser. The `browse` mode serves a file listing page where the user can select a log to open, which creates a replay session.

### 2.4 Engine Integration

For live mode, the engine must be started with its MCP server enabled. The engine spec (v4, section 9.9) already defines the MCP tools required. The control server connects as an MCP client.

The engine additionally exposes a **Server-Sent Events (SSE) endpoint** for push notifications:

```
GET /events/stream
```

This streams tick-completion events as they happen, allowing the UI to update in real-time without polling. Each SSE message contains the tick number and a summary of what changed (entity count, action count, score). The UI then fetches full event data via the REST API if the user is watching that tick.

For the SSE endpoint to work, the engine binary must include a small HTTP handler alongside its MCP server. This is a one-time addition to the engine's server mode:

```go
// In the engine's server mode setup
w.EnableUI(we.UIConfig{
    SSEPath: "/events/stream",
    Port:    9090,
})
```

---

## 3. Control API

The control server exposes a REST + WebSocket API that the browser UI consumes. This API abstracts over live and replay sessions — the UI doesn't need to know which mode it's in for most operations.

### 3.1 Session Management

```
POST   /api/sessions                    Create a session (live or replay)
GET    /api/sessions                    List active sessions
GET    /api/sessions/:id                Get session info (mode, world name, tick range, status)
DELETE /api/sessions/:id                Close a session
```

**Create session request:**

```json
POST /api/sessions
{
  "mode": "replay",
  "source": "./runs/fishing_economy_20260403T141500_a1b2c3.db"
}
```

```json
POST /api/sessions
{
  "mode": "live",
  "source": "localhost:9090"
}
```

**Session info response:**

```json
{
  "id": "s_abc123",
  "mode": "replay",
  "world_name": "fishing_economy",
  "tick_range": { "min": 0, "max": 365 },
  "current_tick": 0,
  "status": "ready",
  "config": {
    "dt": 1.0,
    "tick_unit": "day",
    "max_ticks": 365,
    "max_actions_per_tick": 3
  }
}
```

Status values: `ready`, `running`, `paused`, `completed`, `error`.

### 3.2 Simulation Control (Live Mode Only)

These endpoints proxy to the engine's MCP tools. They return `405 Method Not Allowed` in replay mode.

```
POST /api/sessions/:id/step            Step forward by N ticks
POST /api/sessions/:id/run             Run until condition or max ticks
POST /api/sessions/:id/pause           Pause a running simulation
POST /api/sessions/:id/resume          Resume a paused simulation
PUT  /api/sessions/:id/resource        Modify an entity's resource
```

**Step request:**

```json
POST /api/sessions/:id/step
{ "ticks": 10 }
```

**Step response:**

```json
{
  "previous_tick": 42,
  "current_tick": 52,
  "elapsed_ms": 34
}
```

**Set resource request:**

```json
PUT /api/sessions/:id/resource
{
  "entity_id": "boat_1",
  "resource": "fuel",
  "value": 100.0
}
```

### 3.3 State Queries (Both Modes)

```
GET /api/sessions/:id/state?tick=N                  Full world state at tick N
GET /api/sessions/:id/entities?tick=N               Entity list with types and locations
GET /api/sessions/:id/entities/:eid?tick=N          Single entity detail (params, resources, location)
GET /api/sessions/:id/connections?tick=N             Connection graph at tick N
GET /api/sessions/:id/events?from=A&to=B            Events in tick range
GET /api/sessions/:id/events?from=A&to=B&entity=E   Events filtered by entity
GET /api/sessions/:id/scores?agent=A                Score history for an agent
GET /api/sessions/:id/types                         Type definitions (schema, visibility)
```

When `tick` is omitted, the current tick is used.

**Entity detail response:**

```json
{
  "entity_id": "boat_1",
  "type": "PlayerBoat",
  "params": { "speed": 5.0 },
  "resources": { "fuel": 72.3, "catch": 15.0, "money": 870.0 },
  "location": "lake_alpha",
  "connections": [
    { "to": "port_alpha", "type": "shore_access", "weight": 1.0 }
  ]
}
```

**Events response:**

```json
{
  "events": [
    {
      "tick": 42,
      "phase": "action",
      "event_type": "resource_set",
      "entity_id": "boat_1",
      "field": "fuel",
      "old_value": 73.3,
      "new_value": 72.3,
      "meta": { "source": "tick" }
    },
    {
      "tick": 42,
      "phase": "action",
      "event_type": "action_invoked",
      "entity_id": "lake_alpha",
      "field": null,
      "old_value": null,
      "new_value": null,
      "meta": {
        "action": "fish",
        "invoker_id": "boat_1",
        "target_id": "lake_alpha",
        "params": { "skill": 5 },
        "result": "ok"
      }
    }
  ]
}
```

### 3.4 Run Log Management

```
GET  /api/runs                         List available run log files in the configured directory
GET  /api/runs/:filename/meta          Peek at run metadata without opening a full session
```

**List runs response:**

```json
{
  "runs": [
    {
      "filename": "fishing_economy_20260403T141500_a1b2c3.db",
      "world_name": "fishing_economy",
      "started_at": "2026-04-03T14:15:00Z",
      "final_tick": 365,
      "status": "completed",
      "size_bytes": 3145728
    }
  ]
}
```

### 3.5 WebSocket — Live Updates

```
WS /api/sessions/:id/ws
```

The WebSocket pushes events from the engine (live mode) or from playback (replay mode during auto-play). Messages are JSON with a `type` field:

```json
{ "type": "tick", "tick": 43, "entity_count": 7, "action_count": 3, "scores": { "boat_1": 542.1 } }
```

```json
{ "type": "event", "tick": 43, "event_type": "entity_moved", "entity_id": "boat_1", "meta": { ... } }
```

```json
{ "type": "status", "status": "paused" }
```

In live mode, these are pushed as the engine produces them. In replay mode during auto-play, the control server reads events from the log and pushes them at the configured playback speed.

### 3.6 Playback Control (Both Modes)

These control the UI's playback — scrubbing through history in either mode, or controlling auto-play speed.

```
POST /api/sessions/:id/seek            Jump to a specific tick
POST /api/sessions/:id/playback        Set playback mode and speed
```

**Seek request:**

```json
POST /api/sessions/:id/seek
{ "tick": 200 }
```

**Playback request:**

```json
POST /api/sessions/:id/playback
{
  "mode": "play",
  "speed": 5.0,
  "direction": "forward"
}
```

Playback modes: `play` (auto-advance at speed), `stop` (hold at current tick). Speed is a multiplier: `1.0` = one tick per real-time tick unit (e.g., one tick per second if `tick_unit` is "second"), `5.0` = 5x, `0.5` = half speed. Direction: `forward` or `backward`.

In live mode, `play` with `forward` direction tells the engine to run continuously (proxying to MCP `run`). In replay mode, `play` tells the control server to push events from the log at the configured speed.

Backward playback in both modes reconstructs state at progressively earlier ticks using the State Reconstructor. The control server pre-fetches upcoming states during backward playback to maintain smooth scrubbing.

---

## 4. UI Layout

### 4.1 Overall Structure

The UI is a single-page application with four main regions:

```
┌──────────────────────────────────────────────────────────────┐
│  Toolbar                                                     │
│  [Session: fishing_economy] [Tick: 42/365] [▶ ⏸ ⏹] [1x ▼]  │
├──────────────┬───────────────────────────┬───────────────────┤
│              │                           │                   │
│  Entity Tree │     Main Pane             │   Event Feed      │
│              │  (Detail / Graph / Score) │                   │
│              │                           │                   │
│              │                           │                   │
│              │                           │                   │
│              │                           │                   │
│              │                           │                   │
│              │                           │                   │
├──────────────┴───────────────────────────┴───────────────────┤
│  Timeline                                                    │
│  ◀ [====●============================] ▶   Tick 42 / 365    │
└──────────────────────────────────────────────────────────────┘
```

All panels are resizable via drag handles. The main pane has tabs for switching between views (Detail, Graph, Score). The entity tree and event feed are always visible.

### 4.2 Toolbar

The toolbar shows session metadata and simulation controls.

**Session info:** World name, current tick, total ticks, tick unit, session mode (live/replay) indicated by a badge.

**Transport controls:** Play, pause, stop, step forward, step backward. In replay mode, all are available. In live mode, step backward uses state reconstruction (the engine's live state doesn't rewind — the UI just displays the reconstructed historical state). The play button in live mode advances the simulation; in replay mode it auto-plays through the log.

**Speed selector:** Dropdown or scrubber for playback speed: 0.25x, 0.5x, 1x, 2x, 5x, 10x, 50x, max. "Max" in replay mode scrubs as fast as the control server can reconstruct states.

**Mode indicator:** "LIVE" (green) or "REPLAY" (blue) badge. In live mode, an additional indicator shows whether the simulation is running, paused, or completed.

### 4.3 Entity Tree

A collapsible tree in the left panel showing all entities organized by containment hierarchy.

**Structure:**

```
▼ lake_alpha (FishingGround)
    boat_1 (PlayerBoat) ★
    npc_1 (NPCBoat)
▼ lake_beta (FishingGround)
    npc_2 (NPCBoat)
▶ port_alpha (Port)
▶ port_beta (Port)
```

The `★` marker indicates agent-backed entities. Entity IDs are shown as the primary label with the type name in parentheses. Entities without a container (top-level) appear at root. Clicking an entity selects it and opens its detail in the main pane.

**Visual indicators on tree nodes:**

- Color-coded dot by entity type (consistent colors assigned at session open, derived from type name hash)
- Subtle flash animation when an entity's state changes at the current tick
- Strikethrough for destroyed entities (shown only if the user has scrolled to a tick after destruction; hidden at ticks before spawn)

**Search and filter:** A filter input above the tree supporting text search (matches entity ID and type) and type filtering via a dropdown.

The tree updates as the user scrubs the timeline — entities that don't exist at the current tick are hidden (or shown as greyed-out ghosts if a "show all" toggle is on).

### 4.4 Detail Pane — Entity Detail Tab

When an entity is selected, the detail tab shows its full state at the current tick.

**Sections:**

**Identity:** Entity ID, type name, location (clickable — navigates to the container entity).

**Parameters:** Table of param name/value pairs. Read-only in both modes (params are immutable).

**Resources:** Table of resource name/value pairs. Values that changed at the current tick are highlighted (green for increase, red for decrease, with the delta shown). In live mode, number resources have an inline edit button that triggers the `set_resource` API.

**Connections:** List of connections from this entity showing destination entity (clickable), connection type, and weight.

**Actions this tick:** List of actions this entity was involved in at the current tick (as invoker or target), with result status. Expandable to show params and full metadata.

**History sparkline:** For each numeric resource, a small inline sparkline showing the value over the last N ticks. Clicking the sparkline opens a full chart in the score/chart tab.

### 4.5 Detail Pane — Graph Tab

A force-directed or hierarchical graph visualization showing the connection graph and containment relationships.

**Nodes:** Each entity is a node. Nodes are colored by type (same palette as the tree). Node size can optionally scale by a selected resource value. Agent-backed entities have a distinct border.

**Edges:** Connection graph edges are drawn as lines between nodes, labeled with connection type. Edge thickness scales with weight. Directed connections show an arrowhead.

**Containment:** Entities inside a container are drawn as smaller nodes clustered within or adjacent to their container node, with a subtle containment boundary. This is a visual grouping, not a separate edge type.

**Interaction:**

- Click a node to select it (syncs with entity tree and detail pane)
- Hover a node to highlight its connections
- Drag nodes to rearrange
- Zoom and pan
- Filter by connection type via a toggle panel
- Toggle containment grouping on/off

The graph updates as the user scrubs the timeline — nodes appear/disappear for spawned/destroyed entities, edges change for mutable connections, and node positions within containers update for movement.

**Implementation note:** For worlds with hundreds of entities, a full force-directed layout is expensive. The graph view should support a "focus mode" that shows only the selected entity, its neighbors, and their neighbors (2-hop ego graph). For worlds with thousands of entities, the full graph view should either switch to a grid/matrix layout or show a warning and require the user to opt in.

### 4.6 Detail Pane — Score Tab

A chart view for score and resource timelines.

**Score chart:** Line chart showing score over time for each agent. If multiple agents exist, each gets a line with a distinct color. The x-axis is tick number, the y-axis is score value. A vertical cursor tracks the current tick.

**Resource chart:** When a resource sparkline is clicked in the entity detail, this tab opens with a full line chart for that resource across the simulation. Multiple resources can be overlaid for comparison. A "compare entities" mode allows plotting the same resource across multiple entities (e.g., fuel for all boats).

**Axis controls:** The user can pin the y-axis range or let it auto-scale. The x-axis always spans the full tick range with the timeline scrubber synced.

### 4.7 Event Feed

A scrolling log panel on the right side showing events at the current tick.

**Format:** Each event is a compact line:

```
[42] boat_1: fuel 73.3 → 72.3 (tick)
[42] boat_1 → lake_alpha: fish {skill: 5} → ok
[42] boat_1: moved lake_alpha → port_alpha
[42] order_55: spawned (Order) in market_alpha
[42] order_12: destroyed
```

Events are color-coded by type: resource changes in grey, actions in blue (ok) or orange (failed), movement in purple, lifecycle in green (spawn) or red (destroy).

**Filtering:** Toggle buttons for event types (resources, actions, movement, lifecycle, agent decisions). Entity filter syncs with the tree selection — when an entity is selected, the feed can be filtered to show only events involving that entity.

**History depth:** The feed shows events at the current tick by default. A "show range" toggle expands it to show a configurable window of recent ticks (e.g., last 10 ticks).

**Clicking an event** in the feed selects the relevant entity in the tree and highlights the changed resource in the detail pane.

### 4.8 Timeline

A horizontal scrubber bar along the bottom of the UI.

**Scrubber:** A draggable position indicator on a track spanning tick 0 to max tick. Dragging the scrubber seeks to the target tick. The control server pre-fetches states in the scrub direction to keep reconstruction latency below 100ms.

**Tick markers:** Major ticks every N ticks (auto-scaled to the zoom level), minor ticks between them. Snapshot ticks (from the engine's snapshot interval) are marked with a subtle dot — these reconstruct instantaneously.

**Activity heatmap:** A thin band above the scrubber showing event density per tick, colored by intensity. This gives a quick visual overview of when things are happening — quiet periods are dim, busy periods are bright.

**Minimap markers:** Small colored markers for significant events: entity spawns (green), entity destroys (red), agent failures (orange). These help the user navigate to interesting moments.

**Keyboard shortcuts:** Left/right arrow keys step one tick. Shift+arrow steps 10 ticks. Home/End jump to first/last tick. Space toggles play/pause.

---

## 5. Interaction Patterns

### 5.1 Selecting an Entity

Clicking an entity in the tree, graph, or event feed selects it globally. The selection is synchronized: selecting in one panel highlights in all others. The detail pane updates to show the selected entity's state at the current tick. The event feed optionally filters to that entity.

### 5.2 Scrubbing

Dragging the timeline scrubber (or using arrow keys) changes the current tick. All panels update: the entity tree shows the entity hierarchy at that tick, the detail pane shows resource values at that tick, the graph shows connections at that tick, and the event feed shows events at that tick.

The control server handles scrub requests by calling `get_state_at_tick` on the session, which uses snapshot + delta reconstruction. Aggressive caching (LRU of recently reconstructed states) keeps repeated scrubs fast.

**Pre-fetching strategy:** When the user starts scrubbing in a direction, the control server speculatively reconstructs states for the next several ticks in that direction. For forward scrubbing, this is cheap (apply deltas to the current state). For backward scrubbing, the server reconstructs from the nearest prior snapshot and caches intermediate states along the way.

### 5.3 Auto-Play

Pressing play auto-advances the current tick at the configured speed. In live mode, this tells the engine to run continuously; the UI updates as tick events arrive via WebSocket. In replay mode, the control server reads events from the log and pushes them to the UI at the configured rate.

During auto-play, the entity tree highlights flash as entities change, the event feed scrolls to show new events, the score chart extends, and the timeline scrubber advances. The user can adjust speed during playback.

### 5.4 Live Mode Interactions

In live mode, the user can:

- **Step forward** by N ticks — the engine advances and the UI catches up
- **Pause and resume** — halts the engine at the current tick
- **Modify resources** — edit a resource value on the selected entity (via `set_resource`), then step to see the effect
- **Scrub backward** — the UI reconstructs historical state from the run log; the engine's live state is unaffected
- **Return to live** — after scrubbing backward, a "Return to live" button jumps back to the engine's current tick

Stepping backward in live mode does *not* rewind the engine. It only changes the UI's view tick. The engine continues holding at whatever tick it's paused at. This distinction is surfaced clearly in the toolbar: "Viewing tick 42 (engine at tick 150)".

### 5.5 Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Space` | Play / pause |
| `→` | Step forward 1 tick |
| `←` | Step backward 1 tick |
| `Shift+→` | Step forward 10 ticks |
| `Shift+←` | Step backward 10 ticks |
| `Home` | Jump to tick 0 |
| `End` | Jump to last tick |
| `Escape` | Deselect entity |
| `F` | Toggle fullscreen on main pane |
| `G` | Switch to graph tab |
| `D` | Switch to detail tab |
| `S` | Switch to score tab |
| `/` | Focus entity filter input |

---

## 6. Performance Considerations

### 6.1 State Reconstruction Budget

Target: any tick reconstructable in under 100ms for worlds with fewer than 1,000 entities. This is achievable with the snapshot + delta architecture because:

- Snapshots are at most `SnapshotInterval` ticks apart (default 100)
- Replaying 100 ticks of deltas for 1,000 entities (assuming 20% active) = ~20,000 delta applications
- Each delta application is a map write in Go — well under 1μs each
- Total: ~20ms for reconstruction, plus SQLite query time (~5-20ms for the delta range query)

For larger worlds (10,000 entities), reconstruction may take 50-200ms. The LRU cache and pre-fetching strategy keep interactive scrubbing smooth by avoiding redundant reconstruction.

### 6.2 WebSocket Bandwidth

In live mode, the WebSocket pushes one `tick` summary message per tick plus individual `event` messages. For a world with 50 events per tick running at real-time speed, this is ~50 small JSON messages per tick — trivial bandwidth.

At high playback speeds (50x, max), the UI should batch updates: instead of rendering every tick individually, render every Nth tick and skip intermediate frames. The event feed accumulates but the graph and detail pane only re-render at a throttled rate (e.g., 30fps).

### 6.3 Graph Rendering

The force-directed graph layout is the most expensive UI component. For large worlds:

- Under 50 entities: full force-directed layout, updated live during scrubbing
- 50–500 entities: force-directed layout with physics paused during scrub (positions freeze, only node/edge visibility updates)
- 500+ entities: default to ego-graph mode (2-hop from selected entity), with an option to switch to full grid layout

### 6.4 Entity Tree Rendering

For large entity counts, the tree uses virtualized rendering (only DOM nodes for visible rows). The tree data structure is rebuilt from the state at each tick, but diffed against the previous tick to minimize DOM updates.

---

## 7. Technology Choices

### 7.1 Control Server

- **Language:** Go — same as the engine, uses the engine's `RunLog` API directly
- **HTTP framework:** Standard library `net/http` with a lightweight router
- **WebSocket:** `gorilla/websocket` or `nhooyr.io/websocket`
- **SQLite:** `modernc.org/sqlite` (pure Go, no CGo) for reading run logs
- **Static assets:** Embedded in the binary via `embed` package — single binary deployment

### 7.2 Browser UI

- **Framework:** React (functional components with hooks)
- **State management:** Zustand or built-in React context — the state shape is simple enough to not need Redux
- **Graph rendering:** D3.js force simulation with SVG rendering, or react-flow for a more structured approach
- **Charts:** Recharts or Chart.js for score and resource timelines
- **Tree component:** Virtualized tree with react-window or similar for large entity counts
- **Styling:** Tailwind CSS for utility-first styling, dark theme default with light theme option

### 7.3 Build and Deployment

The UI is built as a static bundle (Vite + React) and embedded into the control server binary at compile time. The result is a single `weui` binary with no runtime dependencies other than the run log `.db` files.

```bash
# Build
cd ui && npm run build
cd .. && go build -o weui ./cmd/weui

# Run
./weui replay ./runs/my_simulation.db
```

---

## 8. File Organization

```
worldengine/
├── cmd/
│   └── weui/               # Control server binary
│       └── main.go
├── pkg/
│   ├── weui/
│   │   ├── server.go        # HTTP + WebSocket server
│   │   ├── session.go        # Session manager (live + replay)
│   │   ├── live_session.go   # Live session — MCP client to engine
│   │   ├── replay_session.go # Replay session — reads run log
│   │   ├── cache.go          # LRU state cache
│   │   └── handlers.go       # REST API handlers
│   └── runlog/              # (already exists from engine spec — Run Log API)
├── ui/                      # React application
│   ├── src/
│   │   ├── App.tsx
│   │   ├── components/
│   │   │   ├── Toolbar.tsx
│   │   │   ├── EntityTree.tsx
│   │   │   ├── DetailPane.tsx
│   │   │   ├── GraphView.tsx
│   │   │   ├── ScoreChart.tsx
│   │   │   ├── EventFeed.tsx
│   │   │   └── Timeline.tsx
│   │   ├── hooks/
│   │   │   ├── useSession.ts
│   │   │   ├── useWebSocket.ts
│   │   │   └── useKeyboard.ts
│   │   ├── api/
│   │   │   └── client.ts     # REST + WebSocket client
│   │   └── store/
│   │       └── index.ts      # Global state (current tick, selected entity, etc.)
│   ├── package.json
│   └── vite.config.ts
└── ...
```

---

## 9. API Error Handling

All REST endpoints return standard error responses:

```json
{
  "error": {
    "code": "session_not_found",
    "message": "No session with ID s_abc123"
  }
}
```

**Error codes:**

| Code | HTTP Status | Meaning |
|------|-------------|---------|
| `session_not_found` | 404 | Session ID doesn't exist |
| `tick_out_of_range` | 400 | Requested tick is outside the session's tick range |
| `live_only` | 405 | Operation only available in live mode |
| `engine_unreachable` | 502 | Live session lost connection to engine MCP server |
| `engine_timeout` | 504 | Engine didn't respond within timeout |
| `log_corrupt` | 500 | Run log file is unreadable or has integrity issues |
| `invalid_request` | 400 | Malformed request body |

---

## 10. Future Considerations

These are out of scope for v1 but worth noting as likely extensions:

**Diff view:** Side-by-side comparison of world state at two different ticks, highlighting what changed. Useful for understanding the impact of a specific action or intervention.

**Multi-session view:** Open two sessions (e.g., two different tournament runs of the same world with different agents) and compare them side by side or overlaid on the same score chart.

**Export:** Export score timelines as CSV, entity state at a tick as JSON, or the graph as an image/SVG.

**Annotations:** User-added markers on the timeline ("agent discovered the port here", "fish stock collapse begins") that persist as metadata in the run log or a sidecar file.

**Query console:** A text input where the user can type arbitrary query language expressions and see results. This would use the engine's `query` MCP tool in live mode, or evaluate queries against reconstructed state in replay mode.

**Agent thought inspector:** For agent-backed entities, show the full perception payload, system prompt, and agent response alongside the action result. This requires the engine to log agent decision payloads, which could be added as an optional field in the `agent_decision` event type.

---

## 11. Summary

This specification defines:

- A **control server** (`weui`) that serves the browser UI and manages live and replay sessions
- A **REST + WebSocket API** providing session management, simulation control, state queries, event streaming, and playback control
- A **browser-based UI** with four main components: entity tree, detail pane (with entity detail, graph, and score tabs), event feed, and timeline scrubber
- **Two operational modes** — live (connected to a running engine) and replay (reading from a run log `.db` file) — sharing the same interface with mode-appropriate controls
- **Interactive scrubbing** through simulation history using the snapshot + delta reconstruction architecture from the engine spec
- **Performance strategies** for state reconstruction (<100ms target), graph rendering (ego-graph fallback for large worlds), and high-speed playback (frame batching)
- A **single-binary deployment** model with static UI assets embedded in the Go control server
- A **file organization** that integrates with the existing engine library codebase
