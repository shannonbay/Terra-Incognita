package steward

import (
	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// archipelagoTick returns the tick function for the Archipelago entity.
func archipelagoTick(w *we.World) func(e *we.Entity, dt float64) {
	return func(e *we.Entity, dt float64) {
		fishStock := e.Get("fish_stock")

		// Logistic growth
		fishStock += fishStock * 0.04 * (1 - fishStock/1000.0)
		fishStock = clamp(fishStock, 0, 1000)

		fishRatio := fishStock / 1000.0
		moratorium := e.Get("moratorium_ticks")

		foodPool := e.Get("food_pool")
		timberPool := e.Get("timber_pool")

		if moratorium > 0 {
			foodPool += 40.0 * fishRatio
			e.Set("moratorium_ticks", moratorium-1)
		} else {
			foodPool += 25.0 * fishRatio
		}

		forestHealthMean := e.Get("forest_health_mean")
		timberPool += 15.0 * (forestHealthMean / 100.0)

		foodPool = clamp(foodPool, 0, 500)
		timberPool = clamp(timberPool, 0, 300)

		e.Set("food_pool", foodPool)
		e.Set("timber_pool", timberPool)

		// Update forest_health_mean from all islands
		allIslands := w.ListEntities("Island")
		if len(allIslands) > 0 {
			var sumForest float64
			for _, island := range allIslands {
				sumForest += island.GetOr("forest_health", 80.0)
			}
			meanForest := sumForest / float64(len(allIslands))
			e.Set("forest_health_mean", meanForest)
		}

		// Fish stock depressed by heavy extraction
		if e.Get("food_pool") < 150 {
			fishStock -= 3.0
		}

		e.Set("fish_stock", clamp(fishStock, 0, 1000))
	}
}

// islandTick returns the tick function for Island entities.
func islandTick(w *we.World) func(e *we.Entity, dt float64) {
	return func(e *we.Entity, dt float64) {
		// Distribute last tick's allocation to islanders
		islanders := e.Contains().Filter(we.Filter{Type: "Islander"})
		count := float64(islanders.Count())
		if count > 0 {
			foodPerCap := e.Get("allocated_food") / count
			timberPerCap := e.Get("allocated_timber") / count
			islanders.Each(func(i *we.Entity) {
				i.Set("food", clamp(i.Get("food")+foodPerCap-1.0, 0, 50))
				i.Set("shelter", clamp(i.Get("shelter")+timberPerCap*0.5-0.3, 0, 50))
			})
		}

		// Reset allocations for this tick
		e.Set("allocated_food", 0)
		e.Set("allocated_timber", 0)

		// Update soil_fertility
		need := e.Get("food_need")
		if need > 25 {
			e.Set("soil_fertility", clamp(e.Get("soil_fertility")-0.5, 10, 100))
		} else {
			e.Set("soil_fertility", clamp(e.Get("soil_fertility")+0.2, 10, 100))
		}

		// Update forest_health
		if e.Get("timber_need") > 15 {
			e.Set("forest_health", clamp(e.Get("forest_health")-0.3, 10, 100))
		} else {
			e.Set("forest_health", clamp(e.Get("forest_health")+0.1, 10, 100))
		}

		// Compute need for next petition cycle
		meanWellbeing := islanders.Avg("wellbeing")
		e.Set("food_need", clamp((60.0-meanWellbeing)*0.6, 0, 50))
		e.Set("timber_need", clamp((50.0-islanders.Avg("shelter"))*0.4, 0, 30))
	}
}

// islanderTick returns the tick function for Islander entities.
func islanderTick(w *we.World) func(e *we.Entity, dt float64) {
	return func(e *we.Entity, dt float64) {
		food := e.Get("food")
		shelter := e.Get("shelter")
		target := clamp(food*1.0+shelter*0.5, 0, 100)
		wellbeing := e.Get("wellbeing")
		wellbeing += (target - wellbeing) * 0.15
		wellbeing -= 0.3
		e.Set("wellbeing", clamp(wellbeing, 0, 100))
	}
}

// stewardTick returns the tick function for the Steward entity.
func stewardTick(w *we.World) func(e *we.Entity, dt float64) {
	return func(e *we.Entity, dt float64) {
		// Authority recovery
		authority := e.Get("authority")
		if authority < 100 {
			authority += 2.0
			if authority > 100 {
				authority = 100
			}
			e.Set("authority", authority)
		}

		// Trust tracks mean islander wellbeing
		mean := w.QueryFloat("/entities[type=Islander]/resources/wellbeing/@mean")
		trust := e.Get("trust")
		if mean > 55 {
			trust += 1.0
			if trust > 100 {
				trust = 100
			}
		} else if mean < 40 {
			trust -= 1.0
			if trust < 0 {
				trust = 0
			}
		}
		e.Set("trust", trust)
	}
}
