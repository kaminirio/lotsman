package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// API-6: investigate rejects empty/garbage refs with a 400 {"error":...} envelope
// before the engine runs, instead of passing them straight through.
func TestInvestigateValidatesRequiredFields(t *testing.T) {
	srv := testServer(t, "") // no SSO -> anonymous admin, so requests pass RBAC
	cases := []struct{ name, body string }{
		{"missing namespace", `{"cluster":"prod","kind":"Deployment","name":"checkout"}`},
		{"empty cluster", `{"cluster":"","namespace":"demo","kind":"Deployment","name":"checkout"}`},
		{"blank name (whitespace)", `{"cluster":"prod","namespace":"demo","kind":"Deployment","name":"   "}`},
		{"all empty", `{}`},
		{"missing kind", `{"cluster":"prod","namespace":"demo","name":"checkout"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(c.body))
			rec := httptest.NewRecorder()
			srv.handleInvestigate(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", rec.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("want {\"error\":...} envelope, got %q", rec.Body.String())
			}
		})
	}
}

// An over-long field is rejected with 400 (length bound).
func TestInvestigateRejectsOverlongField(t *testing.T) {
	srv := testServer(t, "")
	body := `{"cluster":"prod","namespace":"demo","kind":"Deployment","name":"` +
		strings.Repeat("x", maxRefFieldLen+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleInvestigate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-long name: got %d, want 400", rec.Code)
	}
}

// A fully-populated ref passes validation and reaches the engine (which fails
// 502 in tests) — proving valid input is NOT rejected by the new checks.
func TestInvestigateValidRefPassesValidation(t *testing.T) {
	srv := testServer(t, "")
	body := `{"cluster":"prod","namespace":"demo","kind":"Deployment","name":"checkout"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/investigate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleInvestigate(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("valid ref should pass validation and hit the engine (502), got %d", rec.Code)
	}
}

// API-6: the incident-list ?status filter is validated leniently — a known value
// filters, an unknown value is dropped (200 with the unfiltered list) rather than
// 400'd.
func TestListIncidentsStatusFilterLenient(t *testing.T) {
	srv := testServer(t, "", incident("open-1", "prod", "demo")) // status == open

	get := func(status string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/incidents?status="+status, nil)
		rec := httptest.NewRecorder()
		srv.handleListIncidents(rec, req)
		return rec
	}

	// Unknown status -> lenient: dropped filter, 200, incident still returned.
	rec := get("bogus")
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown status should be lenient (200), got %d", rec.Code)
	}
	if got := decodeIncidents(t, rec); len(got) != 1 {
		t.Fatalf("unknown status dropped -> unfiltered list; want 1 incident, got %d", len(got))
	}

	// Known matching status filters through and returns the incident.
	rec = get("open")
	if rec.Code != http.StatusOK {
		t.Fatalf("known status: got %d, want 200", rec.Code)
	}
	if got := decodeIncidents(t, rec); len(got) != 1 {
		t.Fatalf("status=open should return the open incident; got %d", len(got))
	}

	// Known non-matching status filters it out.
	rec = get("closed")
	if got := decodeIncidents(t, rec); len(got) != 0 {
		t.Fatalf("status=closed should exclude the open incident; got %d", len(got))
	}
}

func TestValidateInvestigateRef(t *testing.T) {
	if err := validateInvestigateRef("c", "n", "k", "name"); err != nil {
		t.Errorf("valid ref rejected: %v", err)
	}
	if err := validateInvestigateRef("", "n", "k", "name"); err == nil {
		t.Error("empty cluster must be rejected")
	}
	if err := validateInvestigateRef("c", "n", "k", strings.Repeat("x", maxRefFieldLen+1)); err == nil {
		t.Error("over-long name must be rejected")
	}
}
