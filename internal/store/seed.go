package store

import (
	"context"
	"time"

	"lotsman/internal/model"
)

// Seed inserts a sample incident + clusters so the dev UI renders something
// before real adapters are wired. The scenario is the canonical Lotsman story:
// an ArgoCD deploy shortly followed by a 5xx spike, an OOMKill, and an error-log
// burst — with the deploy ranked as the probable cause (change-first).
//
// REMOVE once the PostgreSQL store + live ingestion land.
func Seed(m *Memory) {
	ctx := context.Background()
	now := time.Now()

	_ = m.SaveCluster(ctx, Cluster{Name: "prod-eu", Env: "prod", Region: "eu-west-1", Connected: true, AgentVersion: "dev"})
	_ = m.SaveCluster(ctx, Cluster{Name: "staging", Env: "stg", Region: "eu-west-1", Connected: true, AgentVersion: "dev"})

	ref := model.ResourceRef{Cluster: "prod-eu", Namespace: "payments", Kind: "Deployment", Name: "payments-api"}
	deployedAt := now.Add(-9 * time.Minute)
	incidentAt := now.Add(-6 * time.Minute)
	change := &model.ChangeRef{
		Source:   "argocd",
		App:      "payments-api",
		Revision: "9f4c2a18b7",
		SyncedAt: deployedAt,
		URL:      "https://argocd.example/applications/payments-api",
	}

	inc := &model.Incident{
		ID:        "inc-sample-payments",
		Resource:  ref,
		Title:     "payments-api: 5xx spike + pod restarts",
		Status:    model.IncidentInvestigating,
		Severity:  model.SeverityCritical,
		OpenedAt:  incidentAt,
		UpdatedAt: now,
		Timeline: []model.Signal{
			{ID: "s1", Kind: model.SignalChange, Source: "argocd", Resource: ref, Timestamp: deployedAt, Severity: model.SeverityInfo, Title: "ArgoCD synced payments-api", Message: "image payments-api:1.42.0 -> 1.43.0", Change: change},
			{ID: "s2", Kind: model.SignalMetric, Source: "victoriametrics", Resource: ref, Timestamp: incidentAt, Severity: model.SeverityError, Title: "HTTP 5xx rate 14% (threshold 5%)"},
			{ID: "s3", Kind: model.SignalK8sEvent, Source: "kubernetes", Resource: ref, Timestamp: incidentAt.Add(30 * time.Second), Severity: model.SeverityError, Title: "BackOff restarting container", Message: "payments-api-7c9 OOMKilled"},
			{ID: "s4", Kind: model.SignalLog, Source: "loki", Resource: ref, Timestamp: incidentAt.Add(45 * time.Second), Severity: model.SeverityError, Title: "panic: nil pointer in chargeHandler"},
		},
		Hypotheses: []model.Hypothesis{
			{Summary: "Deploy of payments-api (rev 9f4c2a18) synced 3m before the incident", Confidence: 0.86, Category: "deploy", Change: change},
			{Summary: "Resource pressure: container OOMKilled after deploy", Confidence: 0.55, Category: "resource"},
		},
	}
	_ = m.SaveIncident(ctx, inc)
}
