package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

// Local, partial mirrors of the control-plane API types. The CLI only decodes
// the fields it renders in table mode; -o json prints the server's raw body
// verbatim, so full fidelity is preserved regardless of these structs.

type resourceRef struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
}

type hypothesis struct {
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
	Category   string  `json:"category"`
}

type incident struct {
	ID         string       `json:"id"`
	Resource   resourceRef  `json:"resource"`
	Title      string       `json:"title"`
	Status     string       `json:"status"`
	Severity   int          `json:"severity"`
	OpenedAt   time.Time    `json:"opened_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	Hypotheses []hypothesis `json:"hypotheses"`
}

// topHypothesis returns a one-line summary of the highest-ranked probable cause
// (hypotheses arrive pre-sorted by the ranker), or a dash when there is none.
func (i incident) topHypothesis() string {
	if len(i.Hypotheses) == 0 {
		return "-"
	}
	h := i.Hypotheses[0]
	return fmt.Sprintf("%s (%.0f%%)", h.Summary, h.Confidence*100)
}

type cluster struct {
	Name      string `json:"name"`
	Env       string `json:"env"`
	Region    string `json:"region"`
	Connected bool   `json:"connected"`
	Mode      string `json:"mode,omitempty"`
}

// severityName maps the numeric severity scale to its label (mirrors
// model.Severity.String); unknown values fall back to "info".
func severityName(s int) string {
	switch s {
	case 3:
		return "critical"
	case 2:
		return "error"
	case 1:
		return "warning"
	default:
		return "info"
	}
}

// renderJSON pretty-prints a raw JSON body.
func renderJSON(w io.Writer, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// Not valid JSON to re-indent; emit as-is rather than fail the command.
		_, err := w.Write(raw)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w)
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// newTabWriter builds a tabwriter with the CLI's standard column style.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// dash returns "-" for an empty string so table cells never render blank.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// renderIncidentSummary prints a single incident as a one-row summary table:
// id, title, status, top hypothesis. Used by investigate and incidents get.
func renderIncidentSummary(w io.Writer, inc incident) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tTITLE\tSTATUS\tSEVERITY\tTOP HYPOTHESIS")
	fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
		dash(inc.ID), dash(inc.Title), dash(inc.Status),
		severityName(inc.Severity), inc.topHypothesis())
	return tw.Flush()
}

// renderIncidentList prints incidents as a table, one row each.
func renderIncidentList(w io.Writer, incs []incident) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tCLUSTER\tTITLE\tSTATUS\tSEVERITY\tOPENED")
	for _, inc := range incs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			dash(inc.ID), dash(inc.Resource.Cluster), dash(inc.Title),
			dash(inc.Status), severityName(inc.Severity), openedAt(inc.OpenedAt))
	}
	if len(incs) == 0 {
		fmt.Fprintln(tw, "(no incidents)")
	}
	return tw.Flush()
}

// renderClusterList prints clusters as a table.
func renderClusterList(w io.Writer, cs []cluster) error {
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "NAME\tENV\tREGION\tCONNECTED\tMODE")
	for _, c := range cs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\n",
			dash(c.Name), dash(c.Env), dash(c.Region), c.Connected, dash(c.Mode))
	}
	if len(cs) == 0 {
		fmt.Fprintln(tw, "(no clusters)")
	}
	return tw.Flush()
}

// openedAt formats a timestamp for table cells; the zero time renders as a dash.
func openedAt(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}
