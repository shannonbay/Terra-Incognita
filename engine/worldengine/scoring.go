package worldengine

// ---------------------------------------------------------------------------
// Per-tick continuous scoring
// ---------------------------------------------------------------------------

// recordContinuousScores is called each tick (Phase 6) to capture per-tick
// continuous score samples for all agent-backed entities.
func (w *World) recordContinuousScores(tick int) {
	if len(w.scoreContinFns) == 0 {
		return
	}
	for id, e := range w.entities {
		if w.registry.AgentConfig(e.typeName) == nil {
			continue
		}
		if w.continuousSamples == nil {
			w.continuousSamples = make(map[string][][]float64)
		}
		if w.continuousSamples[id] == nil {
			w.continuousSamples[id] = make([][]float64, len(w.scoreContinFns))
		}
		for i, sc := range w.scoreContinFns {
			v := sc.fn(w, id, tick)
			w.continuousSamples[id][i] = append(w.continuousSamples[id][i], v)
		}
	}
}

// ---------------------------------------------------------------------------
// Final scoring
// ---------------------------------------------------------------------------

// evaluateTerminalScore sums all terminal (end-of-run) score functions for agentID.
func (w *World) evaluateTerminalScore(agentID string) float64 {
	var total float64
	for _, fn := range w.scoreFns {
		total += fn(w, agentID)
	}
	return total
}

// evaluateContinuousScore aggregates the accumulated per-tick samples for agentID.
func (w *World) evaluateContinuousScore(agentID string) float64 {
	if w.continuousSamples == nil {
		return 0
	}
	samples := w.continuousSamples[agentID]
	if samples == nil {
		return 0
	}
	var total float64
	for i, sc := range w.scoreContinFns {
		if i >= len(samples) {
			break
		}
		total += applyAggregate(samples[i], sc.agg)
	}
	return total
}

// FinalScore returns the combined terminal + aggregated continuous score for agentID.
func (w *World) FinalScore(agentID string) float64 {
	return w.evaluateTerminalScore(agentID) + w.evaluateContinuousScore(agentID)
}

// writeFinalScores writes terminal scores to the run log for all agent entities.
func (w *World) writeFinalScores() {
	if w.runLog == nil {
		return
	}
	for id, e := range w.entities {
		if w.registry.AgentConfig(e.typeName) == nil {
			continue
		}
		score := w.FinalScore(id)
		_ = w.runLog.WriteScore(w.currentTick, id, score)
	}
}

// ---------------------------------------------------------------------------
// Score visibility in agent perception
// ---------------------------------------------------------------------------

// ScoreHint sets the score visibility to Hints and records the hint text.
func (w *World) ScoreHint(text string) {
	w.scoreVis = Hints
	w.scoreHint = text
}

// scorePerceptionEntries returns score-related perception entries for agentID
// based on the world's ScoreVisibility setting. Merged into perception map
// before each agent call.
func (w *World) scorePerceptionEntries(agentID string) map[string]any {
	out := make(map[string]any)
	switch w.scoreVis {
	case Public:
		out["/score/current"] = w.evaluateContinuousScore(agentID)
	case Hints:
		if w.scoreHint != "" {
			out["/score/hint"] = w.scoreHint
		}
	case Hidden:
		// intentionally empty
	}
	return out
}

// ---------------------------------------------------------------------------
// Aggregate helper
// ---------------------------------------------------------------------------

// applyAggregate combines a slice of floats using the given AggregateFunc.
func applyAggregate(vals []float64, agg AggregateFunc) float64 {
	if len(vals) == 0 {
		return 0
	}
	switch agg {
	case AggregateMean:
		var sum float64
		for _, v := range vals {
			sum += v
		}
		return sum / float64(len(vals))
	case AggregateMin:
		min := vals[0]
		for _, v := range vals {
			if v < min {
				min = v
			}
		}
		return min
	case AggregateMax:
		max := vals[0]
		for _, v := range vals {
			if v > max {
				max = v
			}
		}
		return max
	case AggregateSum:
		var sum float64
		for _, v := range vals {
			sum += v
		}
		return sum
	default:
		// fallback: mean
		var sum float64
		for _, v := range vals {
			sum += v
		}
		return sum / float64(len(vals))
	}
}
