// Package detector holds the pluggable conditions that open candidate incidents.
// Detectors run periodically over a cluster scope; their candidates are fed into
// the engine's Investigate flow, which gathers the full timeline and ranks causes.
package detector

import (
	"context"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
)

// Candidate is a potential incident surfaced by a Detector, before correlation.
type Candidate struct {
	Resource model.ResourceRef
	At       time.Time
	Severity model.Severity
	Title    string
	Trigger  model.Signal // the signal that tripped detection
}

// Scope bounds a detection pass to a namespace ("" = all) and time window.
type Scope struct {
	Namespace string
	Range     sources.TimeRange
}

// Detector scans a cluster's sources for conditions worth investigating.
// Implementations must be cheap and side-effect free.
type Detector interface {
	Name() string
	Detect(ctx context.Context, p sources.Provider, scope Scope) ([]Candidate, error)
}
