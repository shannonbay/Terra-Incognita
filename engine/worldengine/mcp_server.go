package worldengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------------------
// MCPServer
// ---------------------------------------------------------------------------

// MCPServer wraps a World and exposes its simulation capabilities as MCP tools.
// Create with NewMCPServer and serve via ServeStdio.
//
// Thread-safety: all tool handlers acquire mu before touching the World.
// The background run goroutine also holds mu for the duration of each tick.
type MCPServer struct {
	mu        sync.Mutex
	world     *World
	server    *mcp.Server
	loadedLog *RunLog
	snapshots map[string]*WorldMemSnapshot
	snapCount int
	running   bool
	pauseCh   chan struct{}
	resumeCh  chan struct{}
	runCtx    context.Context
	runCancel context.CancelFunc
}

// NewMCPServer creates an MCPServer wrapping the given world and registers
// all simulation tools.
func NewMCPServer(w *World) *MCPServer {
	s := &MCPServer{
		world:     w,
		snapshots: make(map[string]*WorldMemSnapshot),
		pauseCh:   make(chan struct{}, 1),
		resumeCh:  make(chan struct{}, 1),
	}
	s.server = mcp.NewServer(&mcp.Implementation{Name: "worldengine", Version: "0.4.0"}, nil)
	s.registerTools()
	return s
}

// ServeStdio runs the MCP server on stdin/stdout until ctx is cancelled.
func (s *MCPServer) ServeStdio(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

// Server returns the underlying mcp.Server (for connecting test clients).
func (s *MCPServer) Server() *mcp.Server { return s.server }

// ---------------------------------------------------------------------------
// Tool registration
// ---------------------------------------------------------------------------

func (s *MCPServer) registerTools() {
	srv := s.server

	// --- Simulation control ---
	mcp.AddTool(srv, &mcp.Tool{Name: "step", Description: "Advance the simulation by N ticks."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			N int `json:"n"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			n := in.N
			if n <= 0 {
				n = 1
			}
			s.mu.Lock()
			tick := s.world.Step(n)
			s.mu.Unlock()
			return nil, map[string]any{"tick": tick}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "step_back", Description: "Reconstruct world state at a past tick from the run log."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Tick int `json:"tick"`
		}) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			state, err := s.world.StateAt(in.Tick)
			s.mu.Unlock()
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				}}, nil, nil
			}
			return nil, state, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "run", Description: "Start running the simulation in the background until max_ticks or pause."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, map[string]any, error) {
			s.mu.Lock()
			already := s.running
			s.mu.Unlock()
			if !already {
				runCtx, cancel := context.WithCancel(ctx)
				s.mu.Lock()
				s.runCtx = runCtx
				s.runCancel = cancel
				s.mu.Unlock()
				go s.runLoop(runCtx)
			}
			return nil, map[string]any{"status": "running"}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "pause", Description: "Pause a background run."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, map[string]any, error) {
			select {
			case s.pauseCh <- struct{}{}:
			default:
			}
			return nil, map[string]any{"status": "paused"}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "resume", Description: "Resume a paused background run."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, map[string]any, error) {
			select {
			case s.resumeCh <- struct{}{}:
			default:
			}
			return nil, map[string]any{"status": "running"}, nil
		})

	// --- World inspection ---
	mcp.AddTool(srv, &mcp.Tool{Name: "query", Description: "Execute a query language expression and return the result."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Expr string `json:"expr"`
		}) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			result := s.world.Query(in.Expr)
			s.mu.Unlock()
			b, _ := json.Marshal(result)
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.TextContent{Text: string(b)},
			}}, nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "list_entities", Description: "List entities, optionally filtered by type."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Type string `json:"type,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			entities := s.world.ListEntities(in.Type)
			s.mu.Unlock()
			out := make([]map[string]any, 0, len(entities))
			for _, e := range entities {
				m := map[string]any{
					"id":   e.id,
					"type": e.typeName,
				}
				out = append(out, m)
			}
			return nil, out, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "set_resource", Description: "Set a resource value on an entity."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Entity string  `json:"entity"`
			Field  string  `json:"field"`
			Value  float64 `json:"value"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			s.mu.Lock()
			e := s.world.Entity(in.Entity)
			if e == nil {
				s.mu.Unlock()
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("entity %q not found", in.Entity)},
				}}, nil, nil
			}
			e.Set(in.Field, in.Value)
			s.mu.Unlock()
			return nil, map[string]any{"ok": true}, nil
		})

	// --- Snapshot / restore ---
	mcp.AddTool(srv, &mcp.Tool{Name: "snapshot", Description: "Save current world state to an in-memory snapshot. Returns the snapshot ID."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, map[string]any, error) {
			s.mu.Lock()
			snap := s.world.MemSnapshot()
			s.snapCount++
			id := fmt.Sprintf("snap_%d", s.snapCount)
			s.snapshots[id] = snap
			s.mu.Unlock()
			return nil, map[string]any{"id": id, "tick": snap.tick}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "restore", Description: "Restore world state from an in-memory snapshot by ID."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID string `json:"id"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			s.mu.Lock()
			snap, ok := s.snapshots[in.ID]
			if !ok {
				s.mu.Unlock()
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("snapshot %q not found", in.ID)},
				}}, nil, nil
			}
			s.world.MemRestore(snap)
			tick := s.world.currentTick
			s.mu.Unlock()
			return nil, map[string]any{"ok": true, "tick": tick}, nil
		})

	// --- Run log (live) ---
	mcp.AddTool(srv, &mcp.Tool{Name: "get_events", Description: "Retrieve events from the current run log for a tick range."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			TickFrom int    `json:"tick_from"`
			TickTo   int    `json:"tick_to"`
			Entity   string `json:"entity,omitempty"`
			Type     string `json:"type,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			log := s.loadedLog
			if log == nil && s.world.runLog != nil {
				// Use the live run log
				log = s.world.runLog
			}
			s.mu.Unlock()
			if log == nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: "no run log available"},
				}}, nil, nil
			}
			events, err := log.Events(EventQuery{
				TickFrom: in.TickFrom,
				TickTo:   in.TickTo,
				EntityID: in.Entity,
				EventType: in.Type,
			})
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				}}, nil, nil
			}
			return nil, events, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "get_state_at_tick", Description: "Reconstruct full world state at an arbitrary tick from the loaded run log."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Tick int `json:"tick"`
		}) (*mcp.CallToolResult, any, error) {
			s.mu.Lock()
			log := s.loadedLog
			s.mu.Unlock()
			if log == nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: "no run log loaded; use load_log first"},
				}}, nil, nil
			}
			state, err := log.StateAt(in.Tick)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				}}, nil, nil
			}
			return nil, state, nil
		})

	// --- Run log management ---
	mcp.AddTool(srv, &mcp.Tool{Name: "load_log", Description: "Load a completed run log (.db file) for replay."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Path string `json:"path"`
		}) (*mcp.CallToolResult, map[string]any, error) {
			log, err := OpenRunLog(in.Path)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				}}, nil, nil
			}
			s.mu.Lock()
			if s.loadedLog != nil {
				_ = s.loadedLog.Close()
			}
			s.loadedLog = log
			s.mu.Unlock()
			return nil, map[string]any{"ok": true, "path": in.Path}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "unload_log", Description: "Close the loaded run log."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, map[string]any, error) {
			s.mu.Lock()
			if s.loadedLog != nil {
				_ = s.loadedLog.Close()
				s.loadedLog = nil
			}
			s.mu.Unlock()
			return nil, map[string]any{"ok": true}, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "list_runs", Description: "List available run log .db files in a directory."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Dir string `json:"dir,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			dir := in.Dir
			if dir == "" {
				dir = "runs"
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: err.Error()},
				}}, nil, nil
			}
			var paths []string
			for _, e := range entries {
				if !e.IsDir() && filepath.Ext(e.Name()) == ".db" {
					paths = append(paths, filepath.Join(dir, e.Name()))
				}
			}
			return nil, paths, nil
		})

	// --- Tournament ---
	mcp.AddTool(srv, &mcp.Tool{Name: "run_tournament",
		Description: "Run a tournament. Provide agents as a JSON array of {name, endpoint, timeout_ms}. Uses the current world as the only world (RunsPerWorld runs)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Agents       []struct {
				Name      string `json:"name"`
				Endpoint  string `json:"endpoint"`
				TimeoutMs int    `json:"timeout_ms"`
			} `json:"agents"`
			RunsPerWorld int    `json:"runs_per_world"`
			Aggregation  string `json:"aggregation,omitempty"`
		}) (*mcp.CallToolResult, any, error) {
			if len(in.Agents) == 0 {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
					&mcp.TextContent{Text: "no agents provided"},
				}}, nil, nil
			}
			runsPerWorld := in.RunsPerWorld
			if runsPerWorld <= 0 {
				runsPerWorld = 1
			}
			agg := AggregateFunc(in.Aggregation)
			if agg == "" {
				agg = AggregateMean
			}
			// Capture the current world config so the factory can recreate it.
			s.mu.Lock()
			worldSnap := s.world.MemSnapshot()
			worldCfg := s.world.config
			s.mu.Unlock()

			tr := NewTournament(TournamentConfig{
				Name:         "mcp-tournament",
				RunsPerWorld: runsPerWorld,
				Aggregation:  agg,
			})
			// Use the current world snapshot as the world factory.
			tr.AddWorld("world", func() *World {
				w := New(worldCfg)
				// Restore entity types and graph from the snapshot.
				// Note: types/tick functions are not in the snapshot — they must
				// be re-registered by the caller. This is a limitation of the MCP
				// tournament tool; full tournament support requires programmatic setup.
				w.MemRestore(worldSnap)
				return w
			})
			for _, a := range in.Agents {
				tr.AddAgent(a.Name, ProviderConfig{
					Endpoint:  a.Endpoint,
					TimeoutMs: a.TimeoutMs,
				})
			}
			results := tr.Run()
			return nil, results, nil
		})
}

// ---------------------------------------------------------------------------
// Background run loop
// ---------------------------------------------------------------------------

func (s *MCPServer) runLoop(ctx context.Context) {
	s.mu.Lock()
	s.running = true
	s.world.initSubsystems()
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.pauseCh:
			// Blocked until resume or ctx cancel.
			select {
			case <-s.resumeCh:
			case <-ctx.Done():
				return
			}
		default:
		}

		s.mu.Lock()
		w := s.world
		maxTicks := w.config.MaxTicks
		cur := w.currentTick
		if maxTicks > 0 && cur >= maxTicks {
			s.mu.Unlock()
			return
		}
		w.tick()
		s.mu.Unlock()
	}
}
