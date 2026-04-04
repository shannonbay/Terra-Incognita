package worldengine

import "fmt"

// ---------------------------------------------------------------------------
// TournamentRunner
// ---------------------------------------------------------------------------

// TournamentRunner runs multiple agents against multiple worlds and aggregates
// scores into a leaderboard.
type TournamentRunner struct {
	cfg    TournamentConfig
	worlds []tournamentWorld
	agents []tournamentAgent
}

type tournamentWorld struct {
	name string
	fn   func() *World
}

type tournamentAgent struct {
	name string
	cfg  ProviderConfig
}

// NewTournament creates a TournamentRunner with the given configuration.
func NewTournament(cfg TournamentConfig) *TournamentRunner {
	return &TournamentRunner{cfg: cfg}
}

// AddWorld registers a world factory function under the given name. The
// factory is called fresh for each run so every run gets an isolated world.
// World functions should use Provider("player", ...) for the controllable
// entity; the tournament overrides "player" with the actual agent config.
func (t *TournamentRunner) AddWorld(name string, fn func() *World) {
	t.worlds = append(t.worlds, tournamentWorld{name: name, fn: fn})
}

// AddAgent registers an agent under the given name with its ProviderConfig.
func (t *TournamentRunner) AddAgent(name string, cfg ProviderConfig) {
	t.agents = append(t.agents, tournamentAgent{name: name, cfg: cfg})
}

// ---------------------------------------------------------------------------
// Results
// ---------------------------------------------------------------------------

// AgentWorldResult holds one agent's scores across all runs of one world.
type AgentWorldResult struct {
	AgentName string
	WorldName string
	Scores    []float64
	Aggregate float64
}

// LeaderboardEntry is one ranked row in the tournament leaderboard.
type LeaderboardEntry struct {
	Rank      int
	AgentName string
	Score     float64 // mean aggregated score across all worlds
}

// TournamentResults holds the complete tournament outcome.
type TournamentResults struct {
	Config      TournamentConfig
	Results     []AgentWorldResult
	Leaderboard []LeaderboardEntry
}

// PrintLeaderboard prints the leaderboard to stdout.
func (r *TournamentResults) PrintLeaderboard() {
	for _, e := range r.Leaderboard {
		fmt.Printf("#%d  %-20s  %.4f\n", e.Rank, e.AgentName, e.Score)
	}
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

// Run executes the tournament — all agents × all worlds × RunsPerWorld — and
// returns the aggregated results sorted by leaderboard score.
func (t *TournamentRunner) Run() *TournamentResults {
	runsPerWorld := t.cfg.RunsPerWorld
	if runsPerWorld <= 0 {
		runsPerWorld = 1
	}
	agg := t.cfg.Aggregation
	if agg == "" {
		agg = AggregateMean
	}

	var results []AgentWorldResult

	for _, agent := range t.agents {
		for _, world := range t.worlds {
			var runScores []float64
			for run := 0; run < runsPerWorld; run++ {
				score := t.runOnce(world, agent)
				runScores = append(runScores, score)
			}
			results = append(results, AgentWorldResult{
				AgentName: agent.name,
				WorldName: world.name,
				Scores:    runScores,
				Aggregate: applyAggregate(runScores, agg),
			})
		}
	}

	leaderboard := t.buildLeaderboard(results, agg)

	return &TournamentResults{
		Config:      t.cfg,
		Results:     results,
		Leaderboard: leaderboard,
	}
}

// runOnce runs a single world × agent simulation and returns the final score.
func (t *TournamentRunner) runOnce(world tournamentWorld, agent tournamentAgent) float64 {
	w := world.fn()
	// Inject the agent as the "player" provider.
	w.Provider("player", agent.cfg)
	w.Run()

	// Sum FinalScore across all agent entities in the world.
	var total float64
	for id, e := range w.entities {
		if w.registry.AgentConfig(e.typeName) == nil {
			continue
		}
		total += w.FinalScore(id)
	}
	return total
}

// buildLeaderboard computes per-agent mean score across all worlds and sorts
// the entries descending.
func (t *TournamentRunner) buildLeaderboard(results []AgentWorldResult, agg AggregateFunc) []LeaderboardEntry {
	agentTotals := make(map[string]float64)
	agentCounts := make(map[string]int)
	for _, r := range results {
		agentTotals[r.AgentName] += r.Aggregate
		agentCounts[r.AgentName]++
	}

	entries := make([]LeaderboardEntry, 0, len(t.agents))
	for _, agent := range t.agents {
		count := agentCounts[agent.name]
		var score float64
		if count > 0 {
			score = agentTotals[agent.name] / float64(count)
		}
		entries = append(entries, LeaderboardEntry{AgentName: agent.name, Score: score})
	}

	// Sort descending by score (insertion sort — small N).
	for i := 1; i < len(entries); i++ {
		key := entries[i]
		j := i - 1
		for j >= 0 && entries[j].Score < key.Score {
			entries[j+1] = entries[j]
			j--
		}
		entries[j+1] = key
	}
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries
}
