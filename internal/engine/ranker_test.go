package engine

import (
	"testing"
	"time"

	"lotsman/internal/model"
)

func TestRankerChangeFirst(t *testing.T) {
	opened := time.Now()
	change := &model.ChangeRef{Source: "argocd", App: "payments-api", Revision: "9f4c2a18b7"}
	inc := &model.Incident{
		OpenedAt: opened,
		Timeline: []model.Signal{
			{Kind: model.SignalChange, Timestamp: opened.Add(-3 * time.Minute), Change: change},
			{Kind: model.SignalK8sEvent, Timestamp: opened, Title: "BackOff restarting", Message: "container OOMKilled"},
		},
	}

	hyps := NewRanker().Rank(inc)
	if len(hyps) == 0 {
		t.Fatal("expected hypotheses, got none")
	}
	if hyps[0].Category != "deploy" {
		t.Fatalf("change-first: expected a deploy hypothesis on top, got %q", hyps[0].Category)
	}
	if hyps[0].Confidence < 0.5 {
		t.Fatalf("recent deploy should rank high, got confidence %.2f", hyps[0].Confidence)
	}

	var hasResource bool
	for _, h := range hyps {
		if h.Category == "resource" {
			hasResource = true
		}
	}
	if !hasResource {
		t.Error("expected a resource-pressure hypothesis for the OOMKilled event")
	}
}

func TestRankerIgnoresOldChanges(t *testing.T) {
	opened := time.Now()
	inc := &model.Incident{
		OpenedAt: opened,
		Timeline: []model.Signal{
			// A change well outside the ranker's ChangeWindow must not be blamed.
			{Kind: model.SignalChange, Timestamp: opened.Add(-2 * time.Hour), Change: &model.ChangeRef{App: "old"}},
		},
	}
	if hyps := NewRanker().Rank(inc); len(hyps) != 0 {
		t.Fatalf("expected no hypotheses for a stale change, got %d", len(hyps))
	}
}
