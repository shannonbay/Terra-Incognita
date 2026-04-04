package worldengine

import "fmt"

// pendingAction is an action queued by an entity's tick function.
type pendingAction struct {
	invokerID string
	name      string
	params    P
}

// pendingSpawn is a deferred entity creation.
type pendingSpawn struct {
	id        string
	typeName  string
	init      Init
	spawnedBy string
}

// pendingDestroy is a deferred entity removal.
type pendingDestroy struct {
	id          string
	destroyedBy string
}

// pendingConnect is a deferred connection addition.
type pendingConnect struct {
	fromID   string
	toID     string
	connType string
	weight   float64
	directed bool
}

// pendingDisconnect is a deferred connection removal.
type pendingDisconnect struct {
	fromID   string
	toID     string
	connType string
}

// World is the top-level simulation container. It holds all type definitions,
// entity instances, connection graph, configuration, and drives the tick loop.
//
// Create a World with New(), configure it by calling Type(), Spawn(), Connect(),
// then call Run() to execute the simulation.
type World struct {
	config    Config
	registry  *TypeRegistry
	entities  map[string]*Entity // id → Entity
	resources *ResourceTable
	graph     *Graph

	// Engine subsystems (initialised on first tick)
	scheduler       *TickScheduler
	dispatcher      *ActionDispatcher
	transitions     *TransitionManager
	lifecycle       *LifecycleManager
	agentDispatcher *AgentDispatcher

	// Simulation state
	currentTick      int
	running          bool
	continuousSamples map[string][][]float64 // agentID → [fnIndex][]float64

	// Per-tick queues — populated during tick execution, drained by the engine.
	pendingActions    []pendingAction
	pendingSpawns     []pendingSpawn
	pendingDestroys   []pendingDestroy
	pendingConnects   []pendingConnect
	pendingDisconnects []pendingDisconnect

	// World-level callbacks
	connDescFn    func(conn Connection) string
	moveCostFn    func(mover *Entity, conn Connection) bool
	scoreFns      []func(w *World, agentID string) float64
	scoreContinFns []scoreContinuous
	scoreVis      ScoreVisibilityLevel
	scoreHint     string
	providers     map[string]ProviderConfig

	// Run log (nil when logging disabled)
	runLog *RunLog
}

type scoreContinuous struct {
	fn  func(w *World, agentID string, tick int) float64
	agg AggregateFunc
}

// New creates and returns a new World with the given configuration.
func New(cfg Config) *World {
	if cfg.Log.SnapshotInterval == 0 && cfg.Log.Enabled {
		cfg.Log.SnapshotInterval = 100
	}
	return &World{
		config:    cfg,
		registry:  newTypeRegistry(),
		entities:  make(map[string]*Entity),
		resources: newResourceTable(),
		graph:     newGraph(),
		providers: make(map[string]ProviderConfig),
	}
}

// Type registers and returns a new entity type with the given name.
// Panics if the name is already registered.
func (w *World) Type(name string) *TypeDef {
	t := &TypeDef{name: name, world: w}
	w.registry.Register(t)
	return t
}

// Spawn creates a new entity instance from a registered type.
// Panics if the type is unknown or the ID is already taken.
func (w *World) Spawn(id string, typeName string, init Init) *Entity {
	if _, exists := w.entities[id]; exists {
		panic("worldengine: entity ID already exists: " + id)
	}
	typeDef := w.registry.MustGet(typeName)

	e := &Entity{id: id, typeName: typeName, world: w}
	w.entities[id] = e

	defaultRes := w.registry.DefaultResources(typeName)
	defaultPar := w.registry.DefaultParams(typeName)
	initRes := init.Resources
	initPar := init.Params
	if initRes == nil {
		initRes = P{}
	}
	if initPar == nil {
		initPar = P{}
	}
	w.resources.InitEntity(id, defaultRes, initRes, defaultPar, initPar)

	if init.Location != "" {
		w.graph.Place(id, init.Location)
	}
	_ = typeDef
	return e
}

// Entity returns the entity with the given ID, or nil if not found.
func (w *World) Entity(id string) *Entity {
	return w.entities[id]
}

// Connect adds a bidirectional typed, weighted edge between two entities.
func (w *World) Connect(fromID, toID, connType string, weight float64) {
	w.graph.AddEdge(fromID, toID, connType, weight, false)
}

// ConnectDirected adds a directed (one-way) typed, weighted edge.
func (w *World) ConnectDirected(fromID, toID, connType string, weight float64) {
	w.graph.AddEdge(fromID, toID, connType, weight, true)
}

// Place puts entity childID inside container parentID.
func (w *World) Place(childID, parentID string) {
	w.graph.Place(childID, parentID)
}

// Provider registers an external agent provider under the given name.
func (w *World) Provider(name string, cfg ProviderConfig) {
	w.providers[name] = cfg
}

// ConnectionDescription registers a function that generates agent-visible
// descriptions for connections.
func (w *World) ConnectionDescription(fn func(conn Connection) string) {
	w.connDescFn = fn
}

// MovementCost registers a global function that applies movement costs.
// Returns true if the move succeeds, false if the mover lacks resources.
func (w *World) MovementCost(fn func(mover *Entity, conn Connection) bool) {
	w.moveCostFn = fn
}

// Score registers an end-of-run scoring function.
func (w *World) Score(fn func(w *World, agentID string) float64) {
	w.scoreFns = append(w.scoreFns, fn)
}

// ScoreContinuous registers a per-tick scoring function with an aggregation method.
func (w *World) ScoreContinuous(fn func(w *World, agentID string, tick int) float64, agg AggregateFunc) {
	w.scoreContinFns = append(w.scoreContinFns, scoreContinuous{fn: fn, agg: agg})
}

// ScoreVisibility controls what information the agent receives about scoring.
func (w *World) ScoreVisibility(level ScoreVisibilityLevel) {
	w.scoreVis = level
}

// Query executes a query-language expression and returns the result.
func (w *World) Query(expr string) any { return w.query(expr) }

// QueryFloat executes a query expected to return a scalar float64.
func (w *World) QueryFloat(expr string) float64 { return w.queryFloat(expr) }

// initSubsystems lazily creates the engine subsystems on first use,
// opens the run log if logging is enabled, and writes the tick-0 snapshot.
func (w *World) initSubsystems() {
	if w.scheduler != nil {
		return
	}
	w.scheduler = newTickScheduler(w)
	w.dispatcher = newActionDispatcher(w)
	w.transitions = newTransitionManager(w)
	w.lifecycle = newLifecycleManager(w)
	w.agentDispatcher = newAgentDispatcher(w)

	if w.config.Log.Enabled && w.runLog == nil {
		rl, err := openRunLog(w.config.Log, w.config.Name)
		if err == nil {
			w.runLog = rl
			_ = rl.WriteMeta(w.config)
			_ = rl.WriteTypes(w)
			_ = rl.WriteInitialState(w)
			_ = rl.WriteSnapshot(0, w)
		}
	}
}

// CurrentTick returns the current simulation tick.
func (w *World) CurrentTick() int { return w.currentTick }

// Step executes exactly N ticks. Returns the tick reached.
// Used by the MCP server's step tool.
func (w *World) Step(n int) int {
	w.initSubsystems()
	for i := 0; i < n; i++ {
		if w.config.MaxTicks > 0 && w.currentTick >= w.config.MaxTicks {
			break
		}
		w.tick()
	}
	return w.currentTick
}

// Run executes the simulation for up to Config.MaxTicks ticks.
func (w *World) Run() {
	w.initSubsystems()
	w.running = true
	for w.running {
		if w.config.MaxTicks > 0 && w.currentTick >= w.config.MaxTicks {
			break
		}
		w.tick()
	}
	w.running = false
	if w.runLog != nil {
		w.writeFinalScores()
		_ = w.runLog.WriteMetaEnd(w.currentTick, "completed")
		_ = w.runLog.Close()
		w.runLog = nil
	}
}

// tick executes one simulation step matching spec §10.2.
func (w *World) tick() {
	dt := w.config.DT
	if dt == 0 {
		dt = 1.0
	}

	// Phase 1: execute all tick functions (sequential, deterministic order).
	w.scheduler.ExecuteTicks(dt)

	// Phase 2: agent provider dispatch — collect actions from external agents.
	agentActions := w.agentDispatcher.Decide(w.currentTick)

	// Phase 3: merge tick-function actions + agent actions, drain queues.
	tickActions, spawns, destroys, connects, disconnects := w.drainQueues()
	tickActions = append(tickActions, agentActions...)

	// Phase 4: dispatch non-move actions.
	dispatched, moveActions := w.dispatcher.Dispatch(tickActions)

	// Phase 5: process built-in move transitions.
	moves := w.transitions.Process(moveActions)

	// Phase 6: record continuous scores.
	w.recordContinuousScores(w.currentTick)

	// Phase 7: flush resource deltas and write events to run log.
	resourceDeltas := w.resources.FlushDeltas()
	if w.runLog != nil {
		_ = w.runLog.FlushTick(w.currentTick, resourceDeltas, dispatched, moves, spawns, destroys, connects, disconnects)
	}

	// Phase 8: apply mutable connection changes.
	for _, c := range connects {
		w.graph.AddEdge(c.fromID, c.toID, c.connType, c.weight, c.directed)
	}
	for _, d := range disconnects {
		w.graph.RemoveEdge(d.fromID, d.toID, d.connType)
	}

	// Apply entity lifecycle changes.
	w.lifecycle.ApplySpawns(spawns)
	w.lifecycle.ApplyDestroys(destroys)

	// Phase 9: write periodic snapshot.
	if w.runLog != nil {
		si := w.config.Log.SnapshotInterval
		if si > 0 && (w.currentTick+1)%si == 0 {
			_ = w.runLog.WriteSnapshot(w.currentTick+1, w)
		}
	}

	w.currentTick++
}

// -----------------------------------------------------------------------------
// Internal queue helpers (called by Entity)
// -----------------------------------------------------------------------------

func (w *World) queueAction(a pendingAction) {
	w.pendingActions = append(w.pendingActions, a)
}

func (w *World) queueSpawn(s pendingSpawn) {
	w.pendingSpawns = append(w.pendingSpawns, s)
}

func (w *World) queueDestroy(d pendingDestroy) {
	w.pendingDestroys = append(w.pendingDestroys, d)
}

func (w *World) queueConnectTo(c pendingConnect) {
	w.pendingConnects = append(w.pendingConnects, c)
}

func (w *World) queueDisconnect(d pendingDisconnect) {
	w.pendingDisconnects = append(w.pendingDisconnects, d)
}

// drainQueues resets all per-tick queues and returns their contents.
// ApplyPendingConnectChanges drains the pending connect/disconnect queues and
// applies them to the graph immediately. The tick engine calls this automatically;
// it is also exposed for tests that need to process queue changes without running
// a full tick.
func (w *World) ApplyPendingConnectChanges() {
	for _, c := range w.pendingConnects {
		w.graph.AddEdge(c.fromID, c.toID, c.connType, c.weight, c.directed)
	}
	for _, d := range w.pendingDisconnects {
		w.graph.RemoveEdge(d.fromID, d.toID, d.connType)
	}
	w.pendingConnects = nil
	w.pendingDisconnects = nil
}

// GraphSnapshot returns a deep copy of the current graph state.
func (w *World) GraphSnapshot() *GraphSnapshot {
	return w.graph.Snapshot()
}

// RestoreGraph replaces the graph with a previously taken snapshot.
func (w *World) RestoreGraph(snap *GraphSnapshot) {
	w.graph.Restore(snap)
}

// WorldMemSnapshot is an in-memory point-in-time world snapshot for the
// MCP snapshot/restore tools. All fields are unexported; use MemSnapshot
// and MemRestore to create and apply snapshots.
type WorldMemSnapshot struct {
	tick      int
	resources map[resourceKey]any
	entities  map[string]string // id → typeName
	graph     *GraphSnapshot
}

// MemSnapshot captures the full mutable world state (resources, graph, entity map,
// tick counter) for later restoration. Safe to call between ticks.
func (w *World) MemSnapshot() *WorldMemSnapshot {
	resSnap := make(map[resourceKey]any, len(w.resources.values))
	for k, v := range w.resources.values {
		resSnap[k] = v
	}
	ents := make(map[string]string, len(w.entities))
	for id, e := range w.entities {
		ents[id] = e.typeName
	}
	return &WorldMemSnapshot{
		tick:      w.currentTick,
		resources: resSnap,
		entities:  ents,
		graph:     w.graph.Snapshot(),
	}
}

// MemRestore rolls the world back to a previously taken snapshot.
// Clears all pending queues, continuous score samples, and resource deltas.
func (w *World) MemRestore(snap *WorldMemSnapshot) {
	w.currentTick = snap.tick
	// Restore resources
	w.resources.values = make(map[resourceKey]any, len(snap.resources))
	for k, v := range snap.resources {
		w.resources.values[k] = v
	}
	w.resources.deltas = nil
	// Restore entity map
	w.entities = make(map[string]*Entity, len(snap.entities))
	for id, typeName := range snap.entities {
		w.entities[id] = &Entity{id: id, typeName: typeName, world: w}
	}
	// Restore graph
	w.graph.Restore(snap.graph)
	// Clear transient state
	w.pendingActions = nil
	w.pendingSpawns = nil
	w.pendingDestroys = nil
	w.pendingConnects = nil
	w.pendingDisconnects = nil
	w.continuousSamples = nil
}

// StateAt reconstructs the world state at a past tick from the run log.
// Returns an error if logging is not enabled.
func (w *World) StateAt(tick int) (*WorldState, error) {
	if w.runLog == nil {
		return nil, fmt.Errorf("worldengine: logging not enabled, cannot reconstruct state at tick %d", tick)
	}
	return w.runLog.StateAt(tick)
}

// RunLogPath returns the file path of the current run log, or "" if logging
// is not enabled.
func (w *World) RunLogPath() string {
	if w.runLog == nil {
		return ""
	}
	return w.runLog.path
}

// ListEntities returns all entities, optionally filtered by type name.
// Pass "" to return all entities.
func (w *World) ListEntities(typeName string) []*Entity {
	out := make([]*Entity, 0, len(w.entities))
	for _, e := range w.entities {
		if typeName == "" || e.typeName == typeName {
			out = append(out, e)
		}
	}
	return out
}

// FlushDeltas returns all buffered resource deltas and resets the buffer.
// Called by the engine at end of each tick to write events to the run log.
func (w *World) FlushDeltas() []Delta {
	return w.resources.FlushDeltas()
}

func (w *World) drainQueues() (actions []pendingAction, spawns []pendingSpawn, destroys []pendingDestroy, connects []pendingConnect, disconnects []pendingDisconnect) {
	actions = w.pendingActions
	spawns = w.pendingSpawns
	destroys = w.pendingDestroys
	connects = w.pendingConnects
	disconnects = w.pendingDisconnects
	w.pendingActions = nil
	w.pendingSpawns = nil
	w.pendingDestroys = nil
	w.pendingConnects = nil
	w.pendingDisconnects = nil
	return
}
