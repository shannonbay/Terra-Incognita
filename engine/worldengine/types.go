package worldengine

// P is the universal parameter/resource map used throughout the API.
// Keys are strings; values can be float64, string, bool, []any, map[string]any,
// or the engine's Set and Queue types.
type P map[string]any

// Float returns the value for key as float64, panicking if absent or wrong type.
func (p P) Float(key string) float64 {
	v, ok := p[key]
	if !ok {
		panic("worldengine: key not found in P: " + key)
	}
	f, ok := v.(float64)
	if !ok {
		panic("worldengine: value for key is not float64: " + key)
	}
	return f
}

// String returns the value for key as string, panicking if absent or wrong type.
func (p P) String(key string) string {
	v, ok := p[key]
	if !ok {
		panic("worldengine: key not found in P: " + key)
	}
	s, ok := v.(string)
	if !ok {
		panic("worldengine: value for key is not string: " + key)
	}
	return s
}

// Bool returns the value for key as bool, panicking if absent or wrong type.
func (p P) Bool(key string) bool {
	v, ok := p[key]
	if !ok {
		panic("worldengine: key not found in P: " + key)
	}
	b, ok := v.(bool)
	if !ok {
		panic("worldengine: value for key is not bool: " + key)
	}
	return b
}

// FloatOr returns the value for key as float64, or def if absent/wrong type.
func (p P) FloatOr(key string, def float64) float64 {
	v, ok := p[key]
	if !ok {
		return def
	}
	f, ok := v.(float64)
	if !ok {
		return def
	}
	return f
}

// -----------------------------------------------------------------------------
// World configuration
// -----------------------------------------------------------------------------

// LogConfig controls how simulation runs are persisted.
type LogConfig struct {
	// Dir is the directory where run log (.db) files are written.
	Dir string

	// SnapshotInterval is the number of ticks between full-state snapshots.
	// 0 disables periodic snapshots (not recommended — reconstruction cost is unbounded).
	// Tick 0 is always snapshotted.
	SnapshotInterval int

	// Enabled controls whether logging is active. Set false for benchmarks
	// where only final scores matter and no observability is needed.
	Enabled bool
}

// Config is the top-level world configuration passed to New().
type Config struct {
	// Name is the world identifier used in log file names and metadata.
	Name string

	// DT is the duration of one tick in world-defined units (e.g. 1.0 = 1 day).
	DT float64

	// TickUnit is a label for the time unit (e.g. "day", "second", "year").
	// Informational only — used in logs and agent context.
	TickUnit string

	// MaxTicks is the maximum number of ticks to simulate.
	MaxTicks int

	// MaxActionsPerTick caps how many actions a single entity can invoke per tick.
	// 0 means unlimited.
	MaxActionsPerTick int

	// Log controls run-log persistence.
	Log LogConfig
}

// -----------------------------------------------------------------------------
// Spawn initialisation
// -----------------------------------------------------------------------------

// Init holds the initial values for an entity created via Spawn().
// Fields not specified here fall back to the type's defaults.
type Init struct {
	// Params overrides the type's default parameter values.
	Params P

	// Resources overrides the type's default resource values.
	Resources P

	// Location is the entity ID of the container this entity starts inside.
	// Leave empty for top-level entities.
	Location string
}

// -----------------------------------------------------------------------------
// Connection graph
// -----------------------------------------------------------------------------

// Connection represents a typed, weighted edge between two entities.
type Connection struct {
	// From is the source entity ID.
	From string

	// To is the destination entity ID.
	To string

	// Type is the semantic label for this connection (e.g. "sea_route", "shore_access").
	Type string

	// Weight is a domain-defined scalar (distance, cost, time, etc.).
	Weight float64

	// Directed indicates whether this is a one-way edge.
	// false = bidirectional (default), true = directed From → To only.
	Directed bool
}

// -----------------------------------------------------------------------------
// Entity query / filtering
// -----------------------------------------------------------------------------

// Filter narrows a set of entities when traversing connections or containers.
type Filter struct {
	// Type restricts results to entities of this type name.
	Type string

	// ConnType restricts traversal to connections of this type.
	ConnType string

	// Predicate is an optional arbitrary test applied to each candidate entity.
	Predicate func(e *Entity) bool
}

// -----------------------------------------------------------------------------
// Actions
// -----------------------------------------------------------------------------

// ActionResult is returned by action handler functions to signal success or failure.
type ActionResult struct {
	ok     bool
	reason string
}

// OK returns a successful ActionResult.
func OK() ActionResult {
	return ActionResult{ok: true}
}

// Fail returns a failed ActionResult with an optional reason.
// Pass an empty string to give no information to the agent.
func Fail(reason string) ActionResult {
	return ActionResult{ok: false, reason: reason}
}

// IsOK reports whether the action succeeded.
func (r ActionResult) IsOK() bool { return r.ok }

// Reason returns the failure reason, or "" on success.
func (r ActionResult) Reason() string { return r.reason }

// -----------------------------------------------------------------------------
// Agent provider
// -----------------------------------------------------------------------------

// AgentConfig configures an entity type backed by an external agent provider.
type AgentConfig struct {
	// Provider is the registered provider name (e.g. "claude", "openai").
	Provider string

	// Prompt is the system-level instruction sent to the agent every tick.
	Prompt string

	// Perception lists query-language expressions that scope the agent's context.
	// The engine evaluates these at decision time and assembles the perception block.
	Perception []string

	// TickFrequency controls how often the agent is consulted.
	// 1 = every tick (default), 5 = every 5 ticks.
	TickFrequency int
}

// ProviderConfig registers an external agent provider endpoint.
type ProviderConfig struct {
	// Endpoint is the HTTP URL that accepts POST /decide requests.
	Endpoint string

	// AuthType is the authentication scheme: "bearer" or "" (none).
	AuthType string

	// TokenEnv is the environment variable name holding the auth token.
	TokenEnv string

	// TimeoutMs is the per-request timeout in milliseconds.
	TimeoutMs int

	// Retries is the number of retry attempts on timeout or transient error.
	Retries int
}

// -----------------------------------------------------------------------------
// Scoring
// -----------------------------------------------------------------------------

// ScoreVisibilityLevel controls what the agent knows about the scoring function.
type ScoreVisibilityLevel int

const (
	// Public means the agent receives the scoring function description.
	Public ScoreVisibilityLevel = iota

	// Hints means the agent receives a natural-language goal description.
	Hints

	// Hidden means the agent receives no information about scoring.
	Hidden
)

// ScoreHint wraps a natural-language goal description for Hints visibility.
type ScoreHint struct {
	level ScoreVisibilityLevel
	text  string
}

// HintsVisibility returns a ScoreHint with the Hints level and the given text.
func HintsVisibility(text string) ScoreHint {
	return ScoreHint{level: Hints, text: text}
}

// AggregateFunc names how per-run scores are combined across tournament runs.
type AggregateFunc string

const (
	AggregateMean AggregateFunc = "mean"
	AggregateMin  AggregateFunc = "min"
	AggregateMax  AggregateFunc = "max"
	AggregateSum  AggregateFunc = "sum"
)

// Aggregate returns an AggregateFunc by name — convenience for tournament config.
func Aggregate(name string) AggregateFunc {
	return AggregateFunc(name)
}

// -----------------------------------------------------------------------------
// Tournament
// -----------------------------------------------------------------------------

// TournamentConfig configures a multi-world, multi-agent tournament.
type TournamentConfig struct {
	// Name is a human-readable label for the tournament.
	Name string

	// RunsPerWorld is how many times each world/agent pair is simulated.
	// Higher values reduce variance.
	RunsPerWorld int

	// Aggregation controls how scores across runs are combined.
	Aggregation AggregateFunc
}
