package engine

import (
	"fmt"
	"testing"
	"time"

	"lotsman/internal/model"
)

// TestRankerChangeWindowBoundary is table-driven over three dt values that probe
// the ChangeWindow boundary. The ranker formula is:
//
//	conf = 0.9 - 0.5 * (dt / ChangeWindow)   when 0 <= dt <= ChangeWindow
//
// This pins the exact inclusion/exclusion behaviour so that future refactors do
// not silently change the boundary semantics.
func TestRankerChangeWindowBoundary(t *testing.T) {
	r := NewRanker()
	opened := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)

	// dt==0: change at exactly the incident time — maximum confidence.
	// dt==ChangeWindow: change at exactly the window boundary — currently INCLUDED
	//   (the guard is `dt > r.ChangeWindow`, so equality is admitted). The expected
	//   confidence is 0.9 - 0.5*1.0 = 0.40.
	// dt==ChangeWindow+1ns: just over boundary — excluded.
	cases := []struct {
		name        string
		dt          time.Duration
		wantN       int     // expected number of hypotheses
		wantApprox  float64 // expected confidence (ignored when wantN==0)
		approxDelta float64
	}{
		{
			name:        "dt=0 admitted max confidence",
			dt:          0,
			wantN:       1,
			wantApprox:  0.9,
			approxDelta: 1e-9,
		},
		{
			// Change at exactly ChangeWindow — the current code uses `dt > r.ChangeWindow`
			// so equality is INCLUDED. PIN this behaviour: if the guard ever changes to >=
			// this test will fail and remind the author to update the comment here.
			name:        "dt=ChangeWindow exactly admitted (boundary included)",
			dt:          r.ChangeWindow,
			wantN:       1,
			wantApprox:  0.40, // 0.9 - 0.5*1.0
			approxDelta: 1e-9,
		},
		{
			// One nanosecond past the window — excluded.
			name:  "dt=ChangeWindow+1ns excluded",
			dt:    r.ChangeWindow + time.Nanosecond,
			wantN: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			inc := &model.Incident{
				OpenedAt: opened,
				Timeline: []model.Signal{
					{
						Kind:      model.SignalChange,
						Timestamp: opened.Add(-tc.dt),
						Change:    &model.ChangeRef{App: "svc", Revision: "deadbeef"},
					},
				},
			}
			hyps := r.Rank(inc)
			if len(hyps) != tc.wantN {
				t.Fatalf("dt=%v: want %d hypotheses, got %d", tc.dt, tc.wantN, len(hyps))
			}
			if tc.wantN == 0 {
				return
			}
			got := hyps[0].Confidence
			if diff := got - tc.wantApprox; diff < -tc.approxDelta || diff > tc.approxDelta {
				t.Fatalf("dt=%v: want confidence ≈%.9f, got %.9f (diff=%e)",
					tc.dt, tc.wantApprox, got, diff)
			}
		})
	}
}

// TestRankerMultiChangeOrdering checks that the ranker sorts two change signals
// (the closer one should rank first) and that a resource-pressure hypothesis
// ranks last.
func TestRankerMultiChangeOrdering(t *testing.T) {
	r := NewRanker()
	opened := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)

	// Two deploys: 2 min and 8 min before the incident.
	// dt=2min  → conf = 0.9 - 0.5*(2/15) ≈ 0.833
	// dt=8min  → conf = 0.9 - 0.5*(8/15) ≈ 0.633
	// OOMKilled (resource) has fixed conf = 0.6, so it ranks below both deploys.
	closeDeploy := model.Signal{
		Kind:      model.SignalChange,
		Timestamp: opened.Add(-2 * time.Minute),
		Title:     "close deploy",
		Change:    &model.ChangeRef{App: "api", Revision: "aaa"},
	}
	farDeploy := model.Signal{
		Kind:      model.SignalChange,
		Timestamp: opened.Add(-8 * time.Minute),
		Title:     "far deploy",
		Change:    &model.ChangeRef{App: "api", Revision: "bbb"},
	}
	// An OOMKilled k8s event.
	oomEvent := model.Signal{
		Kind:    model.SignalK8sEvent,
		Title:   "OOMKilled",
		Message: "container OOMKilled",
	}

	inc := &model.Incident{
		OpenedAt: opened,
		Timeline: []model.Signal{closeDeploy, farDeploy, oomEvent},
	}

	hyps := r.Rank(inc)

	// Expect 3 hypotheses: 2 deploy + 1 resource.
	if len(hyps) != 3 {
		var cats []string
		for _, h := range hyps {
			cats = append(cats, fmt.Sprintf("%s(%.2f)", h.Category, h.Confidence))
		}
		t.Fatalf("expected 3 hypotheses, got %d: %v", len(hyps), cats)
	}

	// hyps[0] must be the closer deploy (higher confidence).
	if hyps[0].Category != "deploy" {
		t.Fatalf("hyps[0] must be deploy, got %q", hyps[0].Category)
	}
	if hyps[0].Change == nil || hyps[0].Change.Revision != "aaa" {
		t.Fatalf("hyps[0] must reference the close deploy (rev=aaa), got %+v", hyps[0].Change)
	}

	// hyps[0].Confidence > hyps[1].Confidence.
	if hyps[0].Confidence <= hyps[1].Confidence {
		t.Fatalf("close deploy confidence %.4f must exceed far deploy confidence %.4f",
			hyps[0].Confidence, hyps[1].Confidence)
	}

	// hyps[1] should also be a deploy (the far one).
	if hyps[1].Category != "deploy" {
		t.Fatalf("hyps[1] must be deploy, got %q", hyps[1].Category)
	}

	// Last hypothesis must be resource pressure.
	last := hyps[len(hyps)-1]
	if last.Category != "resource" {
		t.Fatalf("last hypothesis must be resource, got %q", last.Category)
	}
}
