// Package analyze is an OPTIONAL, off-by-default assistive layer that turns the
// deterministic engine's GROUNDED findings (a model.Incident's timeline +
// ranked hypotheses) into a plain-English root-cause narrative and a triage
// label, using a self-hosted LLM.
//
// This is a control-plane concern over model.Incident only — it never touches a
// backend/source type and is never on the detection hot path (the engine
// produces the incident; this layer merely explains an already-correlated one).
// The narrative is grounded ONLY in the incident's findings to resist
// hallucination: the model is given a compact rendering of the real signals and
// hypotheses and is instructed to invent nothing beyond them.
package analyze

import (
	"context"

	"lotsman/internal/model"
)

// Explanation is the assistive, advisory output for one incident. Every field is
// model-reported and must be treated as a suggestion layered over the
// deterministic findings — not a source of truth.
type Explanation struct {
	Summary    string `json:"summary"`    // 2-4 sentence NL root-cause narrative
	Category   string `json:"category"`   // deploy|resource|config|dependency|network|unknown
	Confidence string `json:"confidence"` // low|medium|high (model self-report, advisory)
	Model      string `json:"model"`      // e.g. "gemma3:4b"
}

// Explainer turns a grounded incident into an assistive Explanation. The API
// layer may hold a nil Explainer or one whose Available() is false; both mean
// "LLM analyzer not configured" and the endpoint responds 503 without erroring.
type Explainer interface {
	Explain(ctx context.Context, inc *model.Incident) (Explanation, error)
	Available() bool // true when an LLM backend is configured
	Model() string
}
