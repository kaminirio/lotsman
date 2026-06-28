package model

import "time"

// IncidentStatus is the lifecycle of an investigation.
type IncidentStatus string

const (
	IncidentOpen          IncidentStatus = "open"
	IncidentInvestigating IncidentStatus = "investigating"
	IncidentResolved      IncidentStatus = "resolved"
	IncidentClosed        IncidentStatus = "closed"
)

// Incident is the central artifact of Lotsman: a resource in trouble, the
// time-ordered evidence (Timeline), and ranked probable causes (Hypotheses).
// This is what BetterStack/Komodor surface as "what broke and why".
type Incident struct {
	ID         string         `json:"id"`
	Resource   ResourceRef    `json:"resource"`
	Title      string         `json:"title"`
	Status     IncidentStatus `json:"status"`
	Severity   Severity       `json:"severity"`
	OpenedAt   time.Time      `json:"opened_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ResolvedAt *time.Time     `json:"resolved_at,omitempty"`

	// Timeline is the correlated set of signals (logs/metrics/changes/events)
	// gathered around the incident window, sorted ascending by Timestamp.
	Timeline []Signal `json:"timeline"`

	// Hypotheses are ranked probable causes produced by the engine's ranker.
	Hypotheses []Hypothesis `json:"hypotheses"`
}

// Hypothesis is a scored probable cause for an incident, with the evidence that
// supports it. Confidence is 0..1.
type Hypothesis struct {
	Summary    string     `json:"summary"`
	Confidence float64    `json:"confidence"`
	Category   string     `json:"category"` // "deploy", "resource", "dependency", "config", ...
	Evidence   []Signal   `json:"evidence"`
	Change     *ChangeRef `json:"change,omitempty"` // set when a deploy is implicated
}
