package worldengine

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Parser tests
// ---------------------------------------------------------------------------

func TestParseQuery_EntitiesWildcard(t *testing.T) {
	ast, err := ParseQuery("/entities")
	if err != nil {
		t.Fatal(err)
	}
	if len(ast.Segments) != 1 {
		t.Fatalf("want 1 segment, got %d", len(ast.Segments))
	}
	seg, ok := ast.Segments[0].(SegEntities)
	if !ok {
		t.Fatalf("want SegEntities, got %T", ast.Segments[0])
	}
	if seg.ID != "" {
		t.Errorf("want empty ID, got %q", seg.ID)
	}
}

func TestParseQuery_EntitiesByID(t *testing.T) {
	ast, err := ParseQuery("/entities/lake1")
	if err != nil {
		t.Fatal(err)
	}
	seg := ast.Segments[0].(SegEntities)
	if seg.ID != "lake1" {
		t.Errorf("want ID=lake1, got %q", seg.ID)
	}
}

func TestParseQuery_EntitiesWithTypePred(t *testing.T) {
	ast, err := ParseQuery("/entities[type=Boat]")
	if err != nil {
		t.Fatal(err)
	}
	seg := ast.Segments[0].(SegEntities)
	if len(seg.Preds) != 1 {
		t.Fatalf("want 1 predicate, got %d", len(seg.Preds))
	}
	p := seg.Preds[0]
	if p.Key != "type" || p.Op != "=" || p.Value != "Boat" {
		t.Errorf("wrong pred: %+v", p)
	}
}

func TestParseQuery_ResourcesField(t *testing.T) {
	// /entities[type=Boat] selects entities by predicate; then /resources/fuel projects the field.
	ast, err := ParseQuery("/entities[type=Boat]/resources/fuel")
	if err != nil {
		t.Fatal(err)
	}
	if len(ast.Segments) != 2 {
		t.Fatalf("want 2 segments, got %d", len(ast.Segments))
	}
	seg, ok := ast.Segments[1].(SegResources)
	if !ok {
		t.Fatalf("want SegResources, got %T", ast.Segments[1])
	}
	if seg.Field != "fuel" {
		t.Errorf("want field=fuel, got %q", seg.Field)
	}
}

func TestParseQuery_Aggregator(t *testing.T) {
	ast, err := ParseQuery("/entities[type=Boat]/resources/fuel/@sum")
	if err != nil {
		t.Fatal(err)
	}
	if ast.Aggregator != "sum" {
		t.Errorf("want aggregator=sum, got %q", ast.Aggregator)
	}
}

func TestParseQuery_NeighborsDepthType(t *testing.T) {
	// Use wildcard predicate so the parser does not consume "neighbors" as an entity ID.
	ast, err := ParseQuery("/entities[type=Lake]/neighbors(depth=2, type=route)")
	if err != nil {
		t.Fatal(err)
	}
	if len(ast.Segments) != 2 {
		t.Fatalf("want 2 segments, got %d", len(ast.Segments))
	}
	seg, ok := ast.Segments[1].(SegNeighbors)
	if !ok {
		t.Fatalf("want SegNeighbors, got %T", ast.Segments[1])
	}
	if seg.Depth != 2 {
		t.Errorf("want depth=2, got %d", seg.Depth)
	}
	if seg.ConnType != "route" {
		t.Errorf("want connType=route, got %q", seg.ConnType)
	}
}

func TestParseQuery_SelfLocation(t *testing.T) {
	ast, err := ParseQuery("/self/location")
	if err != nil {
		t.Fatal(err)
	}
	if len(ast.Segments) != 2 {
		t.Fatalf("want 2 segments, got %d", len(ast.Segments))
	}
	if _, ok := ast.Segments[0].(SegSelf); !ok {
		t.Errorf("want SegSelf, got %T", ast.Segments[0])
	}
	if _, ok := ast.Segments[1].(SegLocation); !ok {
		t.Errorf("want SegLocation, got %T", ast.Segments[1])
	}
}

func TestParseQuery_BadAggregator(t *testing.T) {
	_, err := ParseQuery("/entities/@bogus")
	if err == nil {
		t.Fatal("expected error for unknown aggregator")
	}
}

func TestParseQuery_CompoundPreds(t *testing.T) {
	ast, err := ParseQuery("/entities[type=Boat, resources.fuel>10]")
	if err != nil {
		t.Fatal(err)
	}
	seg := ast.Segments[0].(SegEntities)
	if len(seg.Preds) != 2 {
		t.Fatalf("want 2 preds, got %d", len(seg.Preds))
	}
}

func TestParseQuery_GteOp(t *testing.T) {
	ast, err := ParseQuery("/entities[resources.fuel>=5]")
	if err != nil {
		t.Fatal(err)
	}
	seg := ast.Segments[0].(SegEntities)
	if seg.Preds[0].Op != ">=" {
		t.Errorf("want op>=, got %q", seg.Preds[0].Op)
	}
}

// ---------------------------------------------------------------------------
// Executor tests — integration with a small World
// ---------------------------------------------------------------------------

func buildQueryWorld() *World {
	w := New(Config{MaxTicks: 10})

	lake := w.Type("Lake")
	lake.Resources(P{"fish_stock": 50.0, "depth": 10.0})

	boat := w.Type("Boat")
	boat.Resources(P{"fuel": 30.0, "cargo": 0.0})

	w.Spawn("lake1", "Lake", Init{Resources: P{"fish_stock": 100.0, "depth": 5.0}})
	w.Spawn("lake2", "Lake", Init{Resources: P{"fish_stock": 40.0, "depth": 8.0}})
	w.Spawn("boat1", "Boat", Init{Resources: P{"fuel": 20.0}, Location: "lake1"})
	w.Spawn("boat2", "Boat", Init{Resources: P{"fuel": 5.0}, Location: "lake2"})

	w.Connect("lake1", "lake2", "route", 1.0)

	return w
}

func TestQuery_AllEntities(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities")
	if len(result) != 4 {
		t.Errorf("want 4 entities, got %d", len(result))
	}
}

func TestQuery_EntitiesByType(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities[type=Boat]")
	if len(result) != 2 {
		t.Errorf("want 2 boats, got %d", len(result))
	}
}

func TestQuery_EntitiesByID(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities/lake1")
	if len(result) != 1 || result[0].ID() != "lake1" {
		t.Errorf("want lake1, got %v", result)
	}
}

func TestQuery_ResourceFieldScalar(t *testing.T) {
	w := buildQueryWorld()
	v, err := w.QueryResult("/entities/lake1/resources/fish_stock")
	if err != nil {
		t.Fatal(err)
	}
	if toFloat(v) != 100.0 {
		t.Errorf("want 100, got %v", v)
	}
}

func TestQuery_SumAggregator(t *testing.T) {
	w := buildQueryWorld()
	v := w.QueryFloat("/entities[type=Lake]/resources/fish_stock/@sum")
	if v != 140.0 {
		t.Errorf("want 140, got %v", v)
	}
}

func TestQuery_AvgAggregator(t *testing.T) {
	w := buildQueryWorld()
	v := w.QueryFloat("/entities[type=Lake]/resources/fish_stock/@avg")
	if v != 70.0 {
		t.Errorf("want 70, got %v", v)
	}
}

func TestQuery_CountAggregator(t *testing.T) {
	w := buildQueryWorld()
	v, err := w.QueryResult("/entities[type=Boat]/@count")
	if err != nil {
		t.Fatal(err)
	}
	if v.(int) != 2 {
		t.Errorf("want count=2, got %v", v)
	}
}

func TestQuery_PredicateGt(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities[type=Boat, resources.fuel>10]")
	if len(result) != 1 || result[0].ID() != "boat1" {
		t.Errorf("want [boat1], got %v", result)
	}
}

func TestQuery_Neighbors(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities/lake1/neighbors")
	if len(result) != 1 || result[0].ID() != "lake2" {
		t.Errorf("want [lake2], got %v", result)
	}
}

func TestQuery_NeighborsConnType(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities/lake1/neighbors(type=route)")
	if len(result) != 1 {
		t.Errorf("want 1 neighbor via route, got %d", len(result))
	}
	result2 := w.QueryEntities("/entities/lake1/neighbors(type=sea)")
	if len(result2) != 0 {
		t.Errorf("want 0 neighbors via sea, got %d", len(result2))
	}
}

func TestQuery_Contains(t *testing.T) {
	w := buildQueryWorld()
	result := w.QueryEntities("/entities/lake1/contains")
	if len(result) != 1 || result[0].ID() != "boat1" {
		t.Errorf("want [boat1] inside lake1, got %v", result)
	}
}

func TestQuery_SelfLocation(t *testing.T) {
	w := buildQueryWorld()
	v, err := w.QueryAgentPerception("/self/location", "boat1")
	if err != nil {
		t.Fatal(err)
	}
	entities := toEntities(v)
	if len(entities) != 1 || entities[0].ID() != "lake1" {
		t.Errorf("want boat1's location = lake1, got %v", v)
	}
}

func TestQuery_AvailableActions(t *testing.T) {
	w := New(Config{})
	at := w.Type("Actor")
	at.Action("fish", func(invoker *Entity, target *Entity, params P) ActionResult {
		return OK()
	})
	at.Action("sail", func(invoker *Entity, target *Entity, params P) ActionResult {
		return OK()
	})
	w.Spawn("a1", "Actor", Init{})

	v, err := w.QueryResult("/entities/a1/available_actions")
	if err != nil {
		t.Fatal(err)
	}
	actions, ok := v.([]string)
	if !ok {
		t.Fatalf("want []string, got %T", v)
	}
	if len(actions) != 2 {
		t.Errorf("want 2 actions, got %v", actions)
	}
}

func TestQuery_WorldConfig(t *testing.T) {
	w := New(Config{MaxTicks: 42, DT: 0.5})
	v, err := w.QueryResult("/config")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want map, got %T", v)
	}
	if m["MaxTicks"] != 42 {
		t.Errorf("want MaxTicks=42, got %v", m["MaxTicks"])
	}
}

func TestQuery_VisibilityFiltering(t *testing.T) {
	w := New(Config{})
	ft := w.Type("Fish")
	ft.Resources(P{"public_res": 1.0, "secret": 2.0})
	ft.Hidden("secret")

	w.Spawn("f1", "Fish", Init{})
	w.Spawn("observer", "Fish", Init{})

	// World-level query sees hidden resource
	v, err := w.QueryResult("/entities/f1/resources/secret")
	if err != nil {
		t.Fatal(err)
	}
	if toFloat(v) != 2.0 {
		t.Errorf("world query: want 2.0, got %v", v)
	}

	// Agent query from different entity should not see hidden resource
	v2, _ := w.QueryAgentPerception("/entities/f1/resources/secret", "observer")
	if v2 != nil {
		t.Errorf("agent query: want nil for hidden resource, got %v", v2)
	}
}
