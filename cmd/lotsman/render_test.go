package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderIncidentSummary_Table(t *testing.T) {
	inc := incident{
		ID:       "inc-1",
		Title:    "checkout crashlooping",
		Status:   "open",
		Severity: 3,
		Hypotheses: []hypothesis{
			{Summary: "bad deploy v42", Confidence: 0.9, Category: "deploy"},
			{Summary: "noise", Confidence: 0.1},
		},
	}
	var buf bytes.Buffer
	if err := renderIncidentSummary(&buf, inc); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"inc-1", "checkout crashlooping", "open", "critical", "bad deploy v42 (90%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary output missing %q:\n%s", want, out)
		}
	}
	// Only the top hypothesis is shown, not the runner-up.
	if strings.Contains(out, "noise") {
		t.Errorf("summary should show only the top hypothesis:\n%s", out)
	}
}

func TestRenderIncidentSummary_NoHypotheses(t *testing.T) {
	var buf bytes.Buffer
	if err := renderIncidentSummary(&buf, incident{ID: "inc-2", Title: "t", Status: "open"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "-") {
		t.Errorf("expected dash for missing top hypothesis:\n%s", buf.String())
	}
}

func TestRenderIncidentList_Table(t *testing.T) {
	incs := []incident{
		{ID: "a", Resource: resourceRef{Cluster: "prod"}, Title: "one", Status: "open", Severity: 2},
		{ID: "b", Resource: resourceRef{Cluster: "stg"}, Title: "two", Status: "resolved", Severity: 0},
	}
	var buf bytes.Buffer
	if err := renderIncidentList(&buf, incs); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ID", "CLUSTER", "a", "prod", "error", "b", "stg", "info", "resolved"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderIncidentList_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderIncidentList(&buf, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "(no incidents)") {
		t.Errorf("expected empty marker:\n%s", buf.String())
	}
}

func TestRenderClusterList_Table(t *testing.T) {
	cs := []cluster{
		{Name: "prod", Env: "production", Region: "us-east", Connected: true, Mode: "connected"},
		{Name: "old", Env: "", Region: "", Connected: false},
	}
	var buf bytes.Buffer
	if err := renderClusterList(&buf, cs); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "prod", "production", "us-east", "true", "connected", "old", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("cluster output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderJSON_PrettyPrints(t *testing.T) {
	var buf bytes.Buffer
	raw := json.RawMessage(`{"a":1,"b":[2,3]}`)
	if err := renderJSON(&buf, raw); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "\n  \"a\": 1") {
		t.Errorf("expected indented JSON, got:\n%s", out)
	}
	// Result must remain valid JSON.
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("pretty output not valid JSON: %v", err)
	}
}

func TestSeverityName(t *testing.T) {
	cases := map[int]string{0: "info", 1: "warning", 2: "error", 3: "critical", 99: "info"}
	for in, want := range cases {
		if got := severityName(in); got != want {
			t.Errorf("severityName(%d) = %q, want %q", in, got, want)
		}
	}
}
