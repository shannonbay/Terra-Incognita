package worldengine

// TypeDef holds the schema and behavior registration for an entity type.
// Obtain one via World.Type(name).
type TypeDef struct {
	name          string
	world         *World
	params        P
	resources     P
	hidden        map[string]bool
	private       map[string]bool
	remoteActions map[string]bool
	tickFn        func(e *Entity, dt float64)
	actions       map[string]func(target *Entity, invoker *Entity, p P) ActionResult
	agentCfg      *AgentConfig
}

// Params sets default parameter values for this type.
func (t *TypeDef) Params(defaults P) {
	t.params = defaults
}

// Resources sets default resource values for this type.
func (t *TypeDef) Resources(defaults P) {
	t.resources = defaults
}

// Hidden marks a resource as invisible to external agent perception.
// Tick functions running inside the engine can still read it.
func (t *TypeDef) Hidden(resource string) {
	if t.hidden == nil {
		t.hidden = make(map[string]bool)
	}
	t.hidden[resource] = true
}

// Private marks a resource as visible only to the owning entity's own tick function.
func (t *TypeDef) Private(resource string) {
	if t.private == nil {
		t.private = make(map[string]bool)
	}
	t.private[resource] = true
}

// Tick registers the tick function executed every simulation step.
// If Agent is also configured, the tick function runs first.
func (t *TypeDef) Tick(fn func(e *Entity, dt float64)) {
	t.tickFn = fn
}

// Action registers a local action handler on this type.
// The invoker must be colocated with the target for the action to execute.
func (t *TypeDef) Action(name string, fn func(target *Entity, invoker *Entity, p P) ActionResult) {
	if t.actions == nil {
		t.actions = make(map[string]func(target *Entity, invoker *Entity, p P) ActionResult)
	}
	t.actions[name] = fn
}

// RemoteAction registers an action that can be invoked from any location,
// bypassing the colocation requirement.
func (t *TypeDef) RemoteAction(name string, fn func(target *Entity, invoker *Entity, p P) ActionResult) {
	t.Action(name, fn)
	if t.remoteActions == nil {
		t.remoteActions = make(map[string]bool)
	}
	t.remoteActions[name] = true
}

// Agent configures this type as externally driven by an agent provider.
func (t *TypeDef) Agent(cfg AgentConfig) {
	t.agentCfg = &cfg
}
