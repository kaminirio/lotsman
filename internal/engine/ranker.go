package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"lotsman/internal/model"
)

// Ranker turns a correlated timeline into ranked probable causes. The core
// heuristic is change-first (ADR-0008): a deploy/rollout shortly before an
// incident is the highest-precision root-cause signal — this is the "what
// changed?" intelligence that distinguishes Lotsman from a dashboard.
type Ranker struct {
	ChangeWindow time.Duration // how long after a change we still implicate it
}

// logBurstThreshold is the number of error-or-worse log signals in the incident
// window that constitutes a "burst" worth surfacing as its own hypothesis.
const logBurstThreshold = 5

// NewRanker constructs a Ranker with sensible defaults.
func NewRanker() *Ranker { return &Ranker{ChangeWindow: 15 * time.Minute} }

// Rank produces hypotheses sorted by descending confidence.
func (r *Ranker) Rank(inc *model.Incident) []model.Hypothesis {
	var hyps []model.Hypothesis

	// 1. Change-first — deploys/rollouts shortly before the incident.
	for _, s := range inc.Timeline {
		if s.Kind != model.SignalChange || s.Change == nil {
			continue
		}
		dt := inc.OpenedAt.Sub(s.Timestamp)
		if dt < 0 || dt > r.ChangeWindow {
			continue
		}
		// Closer in time => higher confidence (0.9 down to 0.4 across the window).
		conf := 0.9 - 0.5*(float64(dt)/float64(r.ChangeWindow))
		hyps = append(hyps, model.Hypothesis{
			Summary:    fmt.Sprintf("Deploy of %s (rev %s) synced %s before the incident", s.Change.App, shortRev(s.Change.Revision), dt.Round(time.Second)),
			Confidence: conf,
			Category:   "deploy",
			Evidence:   []model.Signal{s},
			Change:     s.Change,
		})
	}

	// 2. Resource pressure — OOM / eviction among Kubernetes events.
	for _, s := range inc.Timeline {
		if s.Kind != model.SignalK8sEvent {
			continue
		}
		hay := strings.ToLower(s.Title + " " + s.Message)
		if strings.Contains(hay, "oomkilled") || strings.Contains(hay, "evicted") {
			hyps = append(hyps, model.Hypothesis{
				Summary:    fmt.Sprintf("Resource pressure: %s", s.Title),
				Confidence: 0.6,
				Category:   "resource",
				Evidence:   []model.Signal{s},
			})
		}
	}

	// 3. Metric anomaly — a flagged metric spike within the window. This is a
	// corroborating "what's on fire" signal, lower precision than a change, so it
	// must rank below ANY in-window deploy-before-incident (change-first, ADR-0008)
	// and below resource pressure. The deploy confidence decays 0.9→0.4 across the
	// change window, so the metric confidence is capped strictly BELOW that 0.4
	// floor (0.39): even a near-boundary deploy still outranks a metric anomaly.
	for _, s := range inc.Timeline {
		if s.Kind != model.SignalMetric || s.Severity < model.SeverityWarning {
			continue // only flagged spikes, not baseline metric context
		}
		hyps = append(hyps, model.Hypothesis{
			Summary:    fmt.Sprintf("Metric anomaly: %s", s.Title),
			Confidence: 0.39,
			Category:   "metric",
			Evidence:   []model.Signal{s},
		})
	}

	// 4. Log burst — a spike of error/critical log lines in the window. This is a
	// corroborating "what's on fire" symptom, not a cause, so it must rank below
	// any in-window deploy (change-first floor 0.4) and at/under the metric
	// anomaly (0.39): confidence is fixed at 0.35. Only a genuine burst
	// (>= logBurstThreshold lines) qualifies, so ordinary error-log noise does not
	// manufacture a hypothesis.
	var errLogs []model.Signal
	for _, s := range inc.Timeline {
		if s.Kind == model.SignalLog && s.Severity >= model.SeverityError {
			errLogs = append(errLogs, s)
		}
	}
	if len(errLogs) >= logBurstThreshold {
		hyps = append(hyps, model.Hypothesis{
			Summary:    fmt.Sprintf("Log burst: %d error+ log lines in the incident window", len(errLogs)),
			Confidence: 0.35,
			Category:   "logs",
			Evidence:   errLogs,
		})
	}

	sort.SliceStable(hyps, func(i, j int) bool { return hyps[i].Confidence > hyps[j].Confidence })
	return hyps
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}
