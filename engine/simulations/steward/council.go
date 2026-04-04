package steward

import (
	"fmt"

	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

// councilTick returns the tick function for Council entities.
func councilTick(w *we.World) func(e *we.Entity, dt float64) {
	return func(e *we.Entity, dt float64) {
		loyalty := e.Param("loyalty")
		islandIdx := int(e.Param("island_idx"))
		islandID := fmt.Sprintf("island_%d", islandIdx+1)
		island := w.Entity(islandID)
		if island == nil {
			return
		}

		// Accumulate corruption over time (inverse of loyalty)
		corruption := e.Get("corruption")
		corruption += (1.0 - loyalty) * 0.15
		if corruption > 1.0 {
			corruption = 1.0
		}
		e.Set("corruption", corruption)

		// Read actual island need
		actualFoodNeed := island.Get("food_need")
		actualTimberNeed := island.Get("timber_need")

		// Corrupt members inflate petitions
		inflationFactor := 1.0 + (1.0-loyalty)*2.5
		petitionFood := actualFoodNeed * inflationFactor
		petitionTimber := actualTimberNeed * inflationFactor

		e.Set("petition_food", petitionFood)
		e.Set("petition_timber", petitionTimber)

		// Urgency: corrupt members dramatise
		islanders := island.Contains().Filter(we.Filter{Type: "Islander"})
		meanWellbeing := islanders.Avg("wellbeing")
		baseUrgency := clamp((60.0-meanWellbeing)/60.0, 0, 1)
		statedUrgency := baseUrgency + (1.0-loyalty)*0.4
		if statedUrgency > 1.0 {
			statedUrgency = 1.0
		}
		e.Set("petition_urgency", statedUrgency)

		// Influence grows when petitions are granted
		e.Set("influence", min100(e.Get("influence")+loyalty*0.3))

		// Build petition text
		islandName := fmt.Sprintf("Island %d", islandIdx+1)
		var text string
		switch {
		case statedUrgency > 0.7 || (1.0-loyalty) > 0.6:
			text = fmt.Sprintf("URGENT: %s is in crisis. Immediate allocation required.", islandName)
		case statedUrgency > 0.4:
			text = fmt.Sprintf("%s needs additional food support. Conditions are deteriorating.", islandName)
		default:
			text = fmt.Sprintf("%s requests a modest increase in food allocation this cycle.", islandName)
		}

		e.MapSet("petition_msg", "text", text)
		e.MapSet("petition_msg", "island", islandID)
		e.MapSet("petition_msg", "food_amount", petitionFood)
		e.MapSet("petition_msg", "timber_amount", petitionTimber)
	}
}

func min100(v float64) float64 {
	if v > 100 {
		return 100
	}
	return v
}
