package engine

import (
	"testing"
	"time"

	"lotsman/internal/model"
)

// logSignals builds n error-severity log signals in the incident window.
func logSignals(n int, at time.Time) []model.Signal {
	out := make([]model.Signal, n)
	for i := range out {
		out[i] = model.Signal{
			Kind:      model.SignalLog,
			Timestamp: at,
			Severity:  model.SeverityError,
			Message:   "boom",
		}
	}
	return out
}

// TestRankerLogBurst pins ENG-3: a spike of error logs becomes a ranked
// log-burst hypothesis, and it stays below a change-first deploy hypothesis.
func TestRankerLogBurst(t *testing.T) {
	opened := time.Now()
	timeline := logSignals(logBurstThreshold+1, opened.Add(-time.Minute))
	timeline = append(timeline, model.Signal{
		Kind:      model.SignalChange,
		Timestamp: opened.Add(-2 * time.Minute),
		Change:    &model.ChangeRef{App: "api", Revision: "deadbeef"},
	})
	inc := &model.Incident{OpenedAt: opened, Timeline: timeline}

	hyps := NewRanker().Rank(inc)

	var logConf, deployConf float64
	var hasLogs bool
	for _, h := range hyps {
		switch h.Category {
		case "logs":
			hasLogs = true
			logConf = h.Confidence
		case "deploy":
			deployConf = h.Confidence
		}
	}
	if !hasLogs {
		t.Fatalf("expected a log-burst hypothesis, got %+v", hyps)
	}
	if hyps[0].Category != "deploy" {
		t.Fatalf("change-first: deploy must stay on top, got %q", hyps[0].Category)
	}
	if !(deployConf > logConf) {
		t.Fatalf("deploy (%.2f) must outrank log burst (%.2f)", deployConf, logConf)
	}
	if logConf >= 0.4 {
		t.Fatalf("log-burst confidence %.2f must be below the deploy floor 0.4", logConf)
	}
}

// TestRankerLogBurstBelowThreshold proves ordinary error-log noise (below the
// burst threshold) does not manufacture a hypothesis.
func TestRankerLogBurstBelowThreshold(t *testing.T) {
	opened := time.Now()
	inc := &model.Incident{
		OpenedAt: opened,
		Timeline: logSignals(logBurstThreshold-1, opened.Add(-time.Minute)),
	}
	for _, h := range NewRanker().Rank(inc) {
		if h.Category == "logs" {
			t.Fatalf("sub-threshold error logs must not yield a log-burst hypothesis, got %+v", h)
		}
	}
}
