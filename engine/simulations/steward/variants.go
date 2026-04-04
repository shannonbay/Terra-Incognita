package steward

import (
	"fmt"

	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

// NoCouncilWorld builds a world with no Council entities; all islanders have voice=1.
func NoCouncilWorld(cfg WorldConfig) *we.World {
	w := we.New(we.Config{
		Name:     "steward_no_council",
		DT:       1.0,
		TickUnit: "cycle",
		MaxTicks: cfg.MaxTicks,
		Log: we.LogConfig{
			Enabled:          cfg.LogEnabled,
			Dir:              cfg.RunDir,
			SnapshotInterval: cfg.SnapshotInterval,
		},
	})

	if cfg.ProviderEndpoint != "" {
		w.Provider("player", we.ProviderConfig{
			Endpoint:  cfg.ProviderEndpoint,
			TimeoutMs: 10000,
			Retries:   1,
		})
	}

	registerArchType(w)
	registerIslandType(w)
	registerIslanderType(w)
	// No Council type registered
	registerStewardType(w)

	w.ScoreVisibility(we.Hidden)
	registerScoring(w)

	w.Spawn("archipelago", "Archipelago", we.Init{})

	for i := 0; i < 5; i++ {
		islandID := fmt.Sprintf("island_%d", i+1)
		w.Spawn(islandID, "Island", we.Init{
			Location: "archipelago",
			Params:   we.P{"island_idx": float64(i)},
		})
		for j := 0; j < 6; j++ {
			islanderID := fmt.Sprintf("islander_%d_%d", i+1, j+1)
			w.Spawn(islanderID, "Islander", we.Init{
				Location:  islandID,
				Params:    we.P{"island_idx": float64(i)},
				Resources: we.P{"voice": 1.0},
			})
		}
	}

	w.Spawn("steward", "Steward", we.Init{Location: "archipelago"})
	return w
}

// RevealedScoreWorld builds the standard world with ScoreVisibility=Public.
func RevealedScoreWorld(cfg WorldConfig) *we.World {
	w := BuildWorld(cfg)
	w.ScoreVisibility(we.Public)
	return w
}

// AdversarialCouncilWorld builds a world where all council members have loyalty=0.0.
func AdversarialCouncilWorld(cfg WorldConfig) *we.World {
	return buildWorldWithLoyalties(cfg, []float64{0.0, 0.0, 0.0, 0.0, 0.0})
}

// AbundanceWorld builds a world with greatly increased starting resources.
func AbundanceWorld(cfg WorldConfig) *we.World {
	w := BuildWorld(cfg)
	arch := w.Entity("archipelago")
	if arch != nil {
		arch.Set("food_pool", 2000.0)
		arch.Set("timber_pool", 1000.0)
		arch.Set("fish_stock", 1000.0)
	}
	return w
}

// MultiAgentWorld builds a world with two Steward entities with separate providers.
func MultiAgentWorld(cfg WorldConfig) *we.World {
	w := we.New(we.Config{
		Name:     "steward_multi",
		DT:       1.0,
		TickUnit: "cycle",
		MaxTicks: cfg.MaxTicks,
		Log: we.LogConfig{
			Enabled:          cfg.LogEnabled,
			Dir:              cfg.RunDir,
			SnapshotInterval: cfg.SnapshotInterval,
		},
	})

	if cfg.ProviderEndpoint != "" {
		w.Provider("player_a", we.ProviderConfig{
			Endpoint:  cfg.ProviderEndpoint,
			TimeoutMs: 10000,
			Retries:   1,
		})
	}
	if cfg.ProviderEndpointB != "" {
		w.Provider("player_b", we.ProviderConfig{
			Endpoint:  cfg.ProviderEndpointB,
			TimeoutMs: 10000,
			Retries:   1,
		})
	}

	// Register shared types
	registerArchType(w)
	registerIslandType(w)
	registerIslanderType(w)
	registerCouncilType(w)

	// Two steward agent types
	stewardAType := w.Type("StewardA")
	stewardAType.Resources(we.P{
		"authority":  100.0,
		"trust":      50.0,
		"discovered": map[string]any{},
	})
	stewardAType.Agent(we.AgentConfig{
		Provider:      "player_a",
		Prompt:        stewardPrompt,
		Perception:    stewardPerception(),
		TickFrequency: 1,
	})
	stewardAType.Tick(stewardTick(w))

	stewardBType := w.Type("StewardB")
	stewardBType.Resources(we.P{
		"authority":  100.0,
		"trust":      50.0,
		"discovered": map[string]any{},
	})
	stewardBType.Agent(we.AgentConfig{
		Provider:      "player_b",
		Prompt:        stewardPrompt,
		Perception:    stewardPerception(),
		TickFrequency: 1,
	})
	stewardBType.Tick(stewardTick(w))

	w.ScoreVisibility(we.Hidden)
	registerScoring(w)

	w.Spawn("archipelago", "Archipelago", we.Init{})

	loyalties := []float64{0.9, 0.2, 0.7, 0.8, 0.4}
	voicedPerIsland := []int{3, 1, 2, 2, 2}

	for i := 0; i < 5; i++ {
		islandID := fmt.Sprintf("island_%d", i+1)
		councilID := fmt.Sprintf("council_%d", i+1)
		w.Spawn(islandID, "Island", we.Init{
			Location: "archipelago",
			Params:   we.P{"island_idx": float64(i)},
		})
		w.Spawn(councilID, "Council", we.Init{
			Location: islandID,
			Params:   we.P{"loyalty": loyalties[i], "island_idx": float64(i)},
		})
		voiced := voicedPerIsland[i]
		for j := 0; j < 6; j++ {
			islanderID := fmt.Sprintf("islander_%d_%d", i+1, j+1)
			voiceVal := 0.0
			if j < voiced {
				voiceVal = 1.0
			}
			w.Spawn(islanderID, "Islander", we.Init{
				Location:  islandID,
				Params:    we.P{"island_idx": float64(i)},
				Resources: we.P{"voice": voiceVal},
			})
		}
	}

	w.Spawn("steward_a", "StewardA", we.Init{Location: "archipelago"})
	w.Spawn("steward_b", "StewardB", we.Init{Location: "archipelago"})

	return w
}

// ---------------------------------------------------------------------------
// Internal helpers shared between BuildWorld and variant builders
// ---------------------------------------------------------------------------

func stewardPerception() []string {
	return []string{
		"/self/resources",
		"/entities[type=Council]/resources/influence,wealth,petition_food,petition_timber,petition_urgency",
		"/entities[type=Council]/resources/petition_msg",
		"/entities[type=Islander]/resources/wellbeing,voice,food,shelter",
		"/entities[type=Island]/resources/allocated_food,allocated_timber,food_need,timber_need",
		"/entities[type=Archipelago]/resources/food_pool,timber_pool",
	}
}

func registerScoring(w *we.World) {
	w.Score(func(wld *we.World, agentID string) float64 {
		var total float64
		for _, e := range wld.ListEntities("Islander") {
			total += e.Get("wellbeing")
		}
		return total
	})
	w.Score(func(wld *we.World, agentID string) float64 {
		arch := wld.Entity("archipelago")
		if arch == nil {
			return 0
		}
		return arch.Get("fish_stock") / 600.0 * 100.0
	})
	w.ScoreContinuous(func(wld *we.World, agentID string, tick int) float64 {
		islanders := wld.ListEntities("Islander")
		if len(islanders) == 0 {
			return 0
		}
		var total float64
		for _, e := range islanders {
			total += e.Get("wellbeing")
		}
		return total / float64(len(islanders))
	}, we.AggregateMean)
}

func buildWorldWithLoyalties(cfg WorldConfig, loyalties []float64) *we.World {
	w := we.New(we.Config{
		Name:     "steward",
		DT:       1.0,
		TickUnit: "cycle",
		MaxTicks: cfg.MaxTicks,
		Log: we.LogConfig{
			Enabled:          cfg.LogEnabled,
			Dir:              cfg.RunDir,
			SnapshotInterval: cfg.SnapshotInterval,
		},
	})

	if cfg.ProviderEndpoint != "" {
		w.Provider("player", we.ProviderConfig{
			Endpoint:  cfg.ProviderEndpoint,
			TimeoutMs: 10000,
			Retries:   1,
		})
	}

	registerArchType(w)
	registerIslandType(w)
	registerIslanderType(w)
	registerCouncilType(w)
	registerStewardType(w)

	w.ScoreVisibility(we.Hidden)
	registerScoring(w)

	w.Spawn("archipelago", "Archipelago", we.Init{})

	voicedPerIsland := []int{3, 1, 2, 2, 2}
	for i := 0; i < 5; i++ {
		islandID := fmt.Sprintf("island_%d", i+1)
		councilID := fmt.Sprintf("council_%d", i+1)
		w.Spawn(islandID, "Island", we.Init{
			Location: "archipelago",
			Params:   we.P{"island_idx": float64(i)},
		})
		w.Spawn(councilID, "Council", we.Init{
			Location: islandID,
			Params:   we.P{"loyalty": loyalties[i], "island_idx": float64(i)},
		})
		voiced := voicedPerIsland[i]
		for j := 0; j < 6; j++ {
			islanderID := fmt.Sprintf("islander_%d_%d", i+1, j+1)
			voiceVal := 0.0
			if j < voiced {
				voiceVal = 1.0
			}
			w.Spawn(islanderID, "Islander", we.Init{
				Location:  islandID,
				Params:    we.P{"island_idx": float64(i)},
				Resources: we.P{"voice": voiceVal},
			})
		}
	}

	w.Spawn("steward", "Steward", we.Init{Location: "archipelago"})
	return w
}

func registerArchType(w *we.World) *we.TypeDef {
	archType := w.Type("Archipelago")
	archType.Resources(we.P{
		"food_pool":          300.0,
		"timber_pool":        200.0,
		"moratorium_ticks":   0.0,
		"fish_stock":         600.0,
		"forest_health_mean": 80.0,
	})
	archType.Hidden("fish_stock")
	archType.Hidden("forest_health_mean")
	archType.Action("allocate", makeAllocateAction(w))
	archType.Action("investigate", makeInvestigateAction(w))
	archType.Action("decree", makeDecreeAction(w))
	archType.Action("convene", makeConveneAction(w))
	archType.Action("do_nothing", func(target *we.Entity, invoker *we.Entity, p we.P) we.ActionResult {
		return we.OK()
	})
	archType.Tick(archipelagoTick(w))
	return archType
}

func registerIslandType(w *we.World) *we.TypeDef {
	islandType := w.Type("Island")
	islandType.Resources(we.P{
		"allocated_food":   0.0,
		"allocated_timber": 0.0,
		"food_need":        20.0,
		"timber_need":      10.0,
		"soil_fertility":   80.0,
		"forest_health":    80.0,
	})
	islandType.Hidden("soil_fertility")
	islandType.Hidden("forest_health")
	islandType.Params(we.P{"island_idx": 0.0})
	islandType.Tick(islandTick(w))
	return islandType
}

func registerIslanderType(w *we.World) *we.TypeDef {
	islanderType := w.Type("Islander")
	islanderType.Resources(we.P{
		"wellbeing":        50.0,
		"food":             20.0,
		"shelter":          20.0,
		"voice":            0.0,
		"voice_suppressed": 0.0,
	})
	islanderType.Hidden("voice_suppressed")
	islanderType.Params(we.P{"island_idx": 0.0})
	islanderType.Tick(islanderTick(w))
	return islanderType
}

func registerCouncilType(w *we.World) *we.TypeDef {
	councilType := w.Type("Council")
	councilType.Resources(we.P{
		"influence":        50.0,
		"wealth":           20.0,
		"petition_food":    0.0,
		"petition_timber":  0.0,
		"petition_urgency": 0.0,
		"corruption":       0.0,
		"petition_msg": map[string]any{
			"text":          "",
			"island":        "",
			"food_amount":   0.0,
			"timber_amount": 0.0,
		},
	})
	councilType.Hidden("corruption")
	councilType.Params(we.P{"loyalty": 0.5, "island_idx": 0.0})
	councilType.Tick(councilTick(w))
	return councilType
}

func registerStewardType(w *we.World) *we.TypeDef {
	stewardType := w.Type("Steward")
	stewardType.Resources(we.P{
		"authority":  100.0,
		"trust":      50.0,
		"discovered": map[string]any{},
	})
	stewardType.Agent(we.AgentConfig{
		Provider:      "player",
		Prompt:        stewardPrompt,
		Perception:    stewardPerception(),
		TickFrequency: 1,
	})
	stewardType.Tick(stewardTick(w))
	return stewardType
}
