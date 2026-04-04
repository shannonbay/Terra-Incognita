package worldengine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// connectMCP creates an MCPServer for the given world, connects an in-memory
// client, and returns the client session. The test is responsible for cleanup
// via the returned cancel function.
func connectMCP(t *testing.T, w *World) (*mcp.ClientSession, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	srv := NewMCPServer(w)
	t1, t2 := mcp.NewInMemoryTransports()

	_, err := srv.Server().Connect(ctx, t1, nil)
	if err != nil {
		cancel()
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		cancel()
		t.Fatalf("client connect: %v", err)
	}

	return cs, cancel
}

// callTool is a convenience wrapper that calls a tool by name with args.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%q): %v", name, err)
	}
	return res
}

// textOf extracts the first TextContent text from a result.
func textOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// buildMCPWorld returns a simple world with one entity for MCP testing.
func buildMCPWorld() *World {
	w := New(Config{MaxTicks: 10})
	bt := w.Type("Box")
	bt.Resources(P{"count": 0.0})
	bt.Tick(func(e *Entity, dt float64) {
		e.Set("count", e.Get("count")+1)
	})
	w.Spawn("box1", "Box", Init{})
	return w
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestMCP_Step(t *testing.T) {
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	res := callTool(t, cs, "step", map[string]any{"n": 3})
	if res.IsError {
		t.Fatalf("step returned error: %s", textOf(res))
	}

	// Verify tick advanced
	if w.CurrentTick() != 3 {
		t.Errorf("want tick=3, got %d", w.CurrentTick())
	}
	// Count should be 3 (tick function adds 1 per tick)
	if w.Entity("box1").Get("count") != 3 {
		t.Errorf("want count=3, got %.0f", w.Entity("box1").Get("count"))
	}
}

func TestMCP_Query(t *testing.T) {
	w := buildMCPWorld()
	w.Step(2) // count = 2
	cs, cancel := connectMCP(t, w)
	defer cancel()

	res := callTool(t, cs, "query", map[string]any{"expr": "/entities[type=Box]/resources/count"})
	if res.IsError {
		t.Fatalf("query returned error: %s", textOf(res))
	}
	txt := textOf(res)
	if txt == "" {
		t.Error("want non-empty query result")
	}
}

func TestMCP_SetResource(t *testing.T) {
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	res := callTool(t, cs, "set_resource", map[string]any{
		"entity": "box1",
		"field":  "count",
		"value":  99.0,
	})
	if res.IsError {
		t.Fatalf("set_resource returned error: %s", textOf(res))
	}
	if w.Entity("box1").Get("count") != 99 {
		t.Errorf("want count=99, got %.0f", w.Entity("box1").Get("count"))
	}
}

func TestMCP_SetResource_UnknownEntity(t *testing.T) {
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	res := callTool(t, cs, "set_resource", map[string]any{
		"entity": "nonexistent",
		"field":  "count",
		"value":  1.0,
	})
	if !res.IsError {
		t.Error("want IsError=true for unknown entity")
	}
}

func TestMCP_ListEntities(t *testing.T) {
	w := buildMCPWorld()
	w.Spawn("box2", "Box", Init{})
	cs, cancel := connectMCP(t, w)
	defer cancel()

	res := callTool(t, cs, "list_entities", map[string]any{"type": "Box"})
	if res.IsError {
		t.Fatalf("list_entities returned error: %s", textOf(res))
	}
	txt := textOf(res)
	// Parse the JSON array
	var entities []map[string]any
	_ = json.Unmarshal([]byte(txt), &entities)
	if len(entities) != 2 {
		t.Errorf("want 2 entities, got %d (raw: %s)", len(entities), txt)
	}
}

func TestMCP_SnapshotRestore(t *testing.T) {
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	// Step 5 ticks (count=5), then snapshot.
	callTool(t, cs, "step", map[string]any{"n": 5})
	if w.Entity("box1").Get("count") != 5 {
		t.Fatalf("setup: want count=5 before snapshot")
	}

	snapRes := callTool(t, cs, "snapshot", nil)
	if snapRes.IsError {
		t.Fatalf("snapshot error: %s", textOf(snapRes))
	}
	txt := textOf(snapRes)
	var snapData map[string]any
	_ = json.Unmarshal([]byte(txt), &snapData)
	id, _ := snapData["id"].(string)
	if id == "" {
		t.Fatalf("snapshot returned no id (got: %s)", txt)
	}

	// Step 3 more ticks (count=8).
	callTool(t, cs, "step", map[string]any{"n": 3})
	if w.Entity("box1").Get("count") != 8 {
		t.Fatalf("setup: want count=8 before restore")
	}

	// Restore to snapshot (count should revert to 5).
	restoreRes := callTool(t, cs, "restore", map[string]any{"id": id})
	if restoreRes.IsError {
		t.Fatalf("restore error: %s", textOf(restoreRes))
	}
	if w.Entity("box1").Get("count") != 5 {
		t.Errorf("want count=5 after restore, got %.0f", w.Entity("box1").Get("count"))
	}
}

func TestMCP_Restore_UnknownID(t *testing.T) {
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	res := callTool(t, cs, "restore", map[string]any{"id": "snap_999"})
	if !res.IsError {
		t.Error("want IsError=true for unknown snapshot ID")
	}
}

func TestMCP_ListRuns_EmptyDir(t *testing.T) {
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	// Use a temp dir that exists but has no .db files.
	dir := t.TempDir()
	res := callTool(t, cs, "list_runs", map[string]any{"dir": dir})
	if res.IsError {
		t.Fatalf("list_runs error: %s", textOf(res))
	}
}

func TestMCP_StepBack_NoLog(t *testing.T) {
	// World without logging — step_back should return an error.
	w := buildMCPWorld()
	cs, cancel := connectMCP(t, w)
	defer cancel()

	w.Step(3)
	res := callTool(t, cs, "step_back", map[string]any{"tick": 1})
	if !res.IsError {
		t.Error("want IsError=true when logging is disabled")
	}
}
