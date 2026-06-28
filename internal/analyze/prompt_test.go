package analyze

import (
	"strings"
	"testing"
	"time"

	"lotsman/internal/model"
)

// sampleIncident builds a deterministic incident with a ranked deploy hypothesis
// and a small timeline spanning all severities.
func sampleIncident() *model.Incident {
	base := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	return &model.Incident{
		ID:       "inc-1",
		Resource: model.ResourceRef{Cluster: "prod", Namespace: "shop", Kind: "Deployment", Name: "checkout"},
		Title:    "checkout crashlooping",
		Status:   model.IncidentOpen,
		Severity: model.SeverityCritical,
		Hypotheses: []model.Hypothesis{
			{
				Summary:    "ArgoCD deploy of checkout immediately preceded the errors",
				Confidence: 0.82,
				Category:   "deploy",
				Change: &model.ChangeRef{
					Source: "argocd", App: "checkout", Revision: "abc1234",
					SyncedAt: base.Add(-2 * time.Minute),
				},
			},
			{
				Summary:    "memory pressure on the node",
				Confidence: 0.20,
				Category:   "resource",
			},
		},
		Timeline: []model.Signal{
			{Kind: model.SignalChange, Source: "argocd", Severity: model.SeverityInfo, Title: "synced checkout", Timestamp: base.Add(-2 * time.Minute)},
			{Kind: model.SignalLog, Source: "loki", Severity: model.SeverityError, Title: "panic: nil pointer", Message: "goroutine 1 [running]: main.handler", Timestamp: base.Add(-1 * time.Minute)},
			{Kind: model.SignalK8sEvent, Source: "kubernetes", Severity: model.SeverityCritical, Title: "BackOff", Message: "Back-off restarting failed container", Timestamp: base},
			{Kind: model.SignalMetric, Source: "victoriametrics", Severity: model.SeverityWarning, Title: "restart rate up", Timestamp: base.Add(-30 * time.Second)},
		},
	}
}

func TestBuildPromptContainsGroundedFindings(t *testing.T) {
	got := buildPrompt(sampleIncident())

	mustContain := []string{
		"Resource: cluster=prod namespace=shop kind=Deployment name=checkout",
		"Incident: checkout crashlooping [severity=critical status=open]",
		"Ranked hypotheses (most likely first):",
		"1. [category=deploy confidence=0.82] ArgoCD deploy of checkout immediately preceded the errors",
		"change: app=checkout revision=abc1234 syncedAt=2026-06-21T09:58:00Z",
		"2. [category=resource confidence=0.20] memory pressure on the node",
		"Timeline sample (top 4 by severity then recency, of 4 total):",
		"[k8s_event/kubernetes critical] BackOff — Back-off restarting failed container",
		"[log/loki error] panic: nil pointer — goroutine 1 [running]: main.handler",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q\n--- prompt ---\n%s", want, got)
		}
	}

	// Severity ordering: the critical BackOff must render before the error panic,
	// which must render before the warning metric and the info change.
	idxCrit := strings.Index(got, "BackOff")
	idxErr := strings.Index(got, "panic: nil pointer")
	idxWarn := strings.Index(got, "restart rate up")
	idxInfo := strings.Index(got, "synced checkout")
	if !(idxCrit < idxErr && idxErr < idxWarn && idxWarn < idxInfo) {
		t.Errorf("timeline not sorted by severity desc: crit=%d err=%d warn=%d info=%d", idxCrit, idxErr, idxWarn, idxInfo)
	}
}

func TestBuildPromptDeterministic(t *testing.T) {
	inc := sampleIncident()
	if buildPrompt(inc) != buildPrompt(inc) {
		t.Fatal("buildPrompt is not deterministic")
	}
}

func TestBuildPromptEmptyIncident(t *testing.T) {
	got := buildPrompt(&model.Incident{})
	if !strings.Contains(got, "(none produced by the engine)") {
		t.Errorf("expected no-hypotheses note, got:\n%s", got)
	}
	if !strings.Contains(got, "(empty)") {
		t.Errorf("expected empty-timeline note, got:\n%s", got)
	}
}

func TestBuildPromptCapsTimeline(t *testing.T) {
	inc := &model.Incident{Resource: model.ResourceRef{Cluster: "c"}}
	for i := 0; i < 30; i++ {
		inc.Timeline = append(inc.Timeline, model.Signal{
			Kind: model.SignalLog, Source: "loki", Severity: model.SeverityError, Title: "e",
		})
	}
	got := buildPrompt(inc)
	if !strings.Contains(got, "Timeline sample (top 15 by severity then recency, of 30 total):") {
		t.Errorf("expected timeline capped to 15, got:\n%s", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 200); got != "hello" {
		t.Errorf("short string changed: %q", got)
	}
	long := strings.Repeat("a", 250)
	got := truncate(long, promptMaxMessage)
	if r := []rune(got); len(r) != promptMaxMessage+1 { // +1 for the ellipsis rune
		t.Errorf("truncated length = %d, want %d", len(r), promptMaxMessage+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated string missing ellipsis: %q", got)
	}
}
