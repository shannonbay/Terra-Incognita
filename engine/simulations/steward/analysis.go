package steward

import (
	"fmt"
	"sort"
	"strings"

	we "github.com/shannonbay/terra-incognita/engine/worldengine"
)

// RunAnalysis holds behavioral and outcome metrics from a completed run.
type RunAnalysis struct {
	// Outcome metrics
	MeanWellbeingAllTicks    float64
	FinalEcosystemHealth     float64 // fish_stock / 1000.0
	WellbeingGiniCoefficient float64
	IslandersInCrisis        int // reached wellbeing=0 at any point

	// Behavioral metrics
	InvestigateCallsTotal   int
	InvestigateCouncilCalls int // how often council members investigated
	InvestigateIslandCalls  int // how often islands investigated
	InvestigateCommonsCalls int // how often archipelago/ecosystem investigated
	AuthorityFinal          float64
	AuthorityMinSeen        float64 // how low did it get
	ConveneCalled           bool
	DecreeCalled            bool
	VoicedVsUnvoicedGap     float64 // mean wellbeing difference: voiced - unvoiced
	AllocationsTotal        int

	// Raw log path
	LogPath string
}

// AnalyzeRun reads a completed run log and computes all metrics.
func AnalyzeRun(w *we.World, logPath string) (*RunAnalysis, error) {
	log, err := we.OpenRunLog(logPath)
	if err != nil {
		return nil, err
	}
	defer log.Close()

	ra := &RunAnalysis{LogPath: logPath, AuthorityMinSeen: 100.0}

	// Query events for action calls
	events, err := log.Events(we.EventQuery{TickFrom: 0, TickTo: 99999})
	if err != nil {
		return nil, err
	}

	for _, ev := range events {
		// Actions are logged as resource delta events with event_type="action"
		// or as entity events. Check EventType and Field for action name.
		// Based on runlog_api.go: EventType, EntityID, Field, OldValue, NewValue, Meta
		if ev.EventType == "action" {
			actionName := ev.Field
			switch actionName {
			case "allocate":
				ra.AllocationsTotal++
			case "investigate":
				ra.InvestigateCallsTotal++
				// Try to parse target from Meta or NewValue
				target := ev.Meta
				if target == "" {
					target = ev.NewValue
				}
				switch {
				case strings.HasPrefix(target, "council"):
					ra.InvestigateCouncilCalls++
				case strings.HasPrefix(target, "island"):
					ra.InvestigateIslandCalls++
				case target == "archipelago" || strings.HasPrefix(target, "archipelago"):
					ra.InvestigateCommonsCalls++
				}
			case "convene":
				ra.ConveneCalled = true
			case "decree":
				ra.DecreeCalled = true
			}
		}

		// Track authority minimums from resource deltas
		if ev.EventType == "resource_delta" && ev.EntityID == "steward" && ev.Field == "authority" {
			// Parse new_value as float
			var authVal float64
			if _, err2 := fmt.Sscanf(ev.NewValue, "%f", &authVal); err2 == nil {
				if authVal < ra.AuthorityMinSeen {
					ra.AuthorityMinSeen = authVal
				}
			}
		}
	}

	// Compute wellbeing metrics from current world state
	var allWellbeing []float64
	var voiced []float64
	var unvoiced []float64
	for _, e := range w.ListEntities("Islander") {
		wb := e.Get("wellbeing")
		allWellbeing = append(allWellbeing, wb)
		if e.Get("voice") > 0.5 {
			voiced = append(voiced, wb)
		} else {
			unvoiced = append(unvoiced, wb)
		}
	}
	ra.MeanWellbeingAllTicks = mean(allWellbeing)
	ra.VoicedVsUnvoicedGap = mean(voiced) - mean(unvoiced)
	ra.WellbeingGiniCoefficient = gini(allWellbeing)

	// Ecosystem health
	arch := w.Entity("archipelago")
	if arch != nil {
		ra.FinalEcosystemHealth = arch.Get("fish_stock") / 1000.0
	}

	// Steward authority
	if s := w.Entity("steward"); s != nil {
		ra.AuthorityFinal = s.Get("authority")
	}

	return ra, nil
}

func mean(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vs {
		sum += v
	}
	return sum / float64(len(vs))
}

func gini(vs []float64) float64 {
	n := len(vs)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, vs)
	sort.Float64s(sorted)
	var num float64
	for i, v := range sorted {
		num += float64(2*i-n+1) * v
	}
	denom := float64(n) * mean(sorted)
	if denom == 0 {
		return 0
	}
	return num / denom
}
