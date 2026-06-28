package analyze

import (
	"fmt"
	"sort"
	"strings"

	"lotsman/internal/model"
	"lotsman/internal/redact"
)

// systemPrompt instructs the model. It is an SRE assistant grounded ONLY in the
// structured findings it is given for ONE incident — it must not invent causes,
// must pick a category from the fixed set, and must emit strict JSON.
const systemPrompt = `You are an SRE assistant inside Lotsman, a Kubernetes incident-investigation tool.
You are given Lotsman's STRUCTURED correlation findings for ONE incident: the affected resource,
the incident title and severity, the engine's ranked probable-cause hypotheses, and a sample of
the correlated signal timeline.

Explain the MOST LIKELY root cause in 2 to 4 sentences, using ONLY the provided findings.
Do NOT invent causes, services, or events that are not supported by the findings. If the findings
are weak or ambiguous, say so plainly and lower your confidence — never fabricate certainty.

Pick exactly one category from this fixed set: deploy, resource, config, dependency, network, unknown.
Report your confidence as one of: low, medium, high.

Output STRICT JSON and nothing else, with exactly these keys:
{"summary": "<2-4 sentences>", "category": "<one of the fixed set>", "confidence": "low|medium|high"}`

// promptMaxSignals caps how many timeline signals are rendered into the prompt,
// so CPU inference latency stays reasonable and the prompt stays a few KB.
const promptMaxSignals = 15

// promptMaxMessage truncates a signal's message in the rendering.
const promptMaxMessage = 200

// buildPrompt renders an incident into a compact, deterministic user message:
// the resource, the incident header, the ranked hypotheses (with change detail
// when present), and a severity-then-recency sample of the timeline. It is a
// pure function (no clock/RNG/map-iteration nondeterminism) so it is unit
// testable with exact string assertions.
func buildPrompt(inc *model.Incident) string {
	var b strings.Builder

	r := inc.Resource
	fmt.Fprintf(&b, "Resource: cluster=%s namespace=%s kind=%s name=%s\n",
		nz(r.Cluster), nz(r.Namespace), nz(r.Kind), nz(r.Name))
	fmt.Fprintf(&b, "Incident: %s [severity=%s status=%s]\n",
		nz(inc.Title), inc.Severity.String(), nz(string(inc.Status)))

	// Ranked hypotheses, in the engine's given order (already ranked).
	b.WriteString("\nRanked hypotheses (most likely first):\n")
	if len(inc.Hypotheses) == 0 {
		b.WriteString("  (none produced by the engine)\n")
	}
	for i, h := range inc.Hypotheses {
		fmt.Fprintf(&b, "  %d. [category=%s confidence=%.2f] %s\n",
			i+1, nz(h.Category), h.Confidence, nz(h.Summary))
		if h.Change != nil {
			fmt.Fprintf(&b, "     change: app=%s revision=%s syncedAt=%s\n",
				nz(h.Change.App), nz(h.Change.Revision), h.Change.SyncedAt.UTC().Format("2006-01-02T15:04:05Z"))
		}
	}

	// Timeline sample: sort a copy by severity desc, then recency desc, take top N.
	sample := make([]model.Signal, len(inc.Timeline))
	copy(sample, inc.Timeline)
	sort.SliceStable(sample, func(i, j int) bool {
		if sample[i].Severity != sample[j].Severity {
			return sample[i].Severity > sample[j].Severity
		}
		return sample[i].Timestamp.After(sample[j].Timestamp)
	})
	if len(sample) > promptMaxSignals {
		sample = sample[:promptMaxSignals]
	}

	fmt.Fprintf(&b, "\nTimeline sample (top %d by severity then recency, of %d total):\n",
		len(sample), len(inc.Timeline))
	if len(sample) == 0 {
		b.WriteString("  (empty)\n")
	}
	for _, s := range sample {
		fmt.Fprintf(&b, "  [%s/%s %s] %s",
			nz(string(s.Kind)), nz(s.Source), s.Severity.String(), nz(s.Title))
		// Scrub secrets/PII from raw log/event bodies before they cross the trust
		// boundary to the LLM endpoint.
		if msg := truncate(redact.Redact(s.Message), promptMaxMessage); msg != "" {
			fmt.Fprintf(&b, " — %s", msg)
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// nz renders an empty string as a stable placeholder so the prompt never has
// dangling "name=" fragments that read as missing data.
func nz(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// truncate shortens s to at most max runes, appending an ellipsis marker when
// cut. Operates on runes so a multibyte message is never split mid-character.
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
