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

	sort.SliceStable(hyps, func(i, j int) bool { return hyps[i].Confidence > hyps[j].Confidence })
	return hyps
}

func shortRev(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}
