package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"lotsman/internal/engine/detector"
	"lotsman/internal/model"
	"lotsman/internal/sources"
	"lotsman/internal/sources/argocd"
	"lotsman/internal/sources/kubernetes"
	"lotsman/internal/sources/loki"
	"lotsman/internal/sources/victoriametrics"
)

// stubProvider builds a provider whose sources are all unimplemented stubs; the
// correlator tolerates their ErrNotImplemented and still yields an incident.
func stubProvider(t *testing.T, cluster string) sources.Provider {
	t.Helper()
	kube, err := kubernetes.New(cluster, "")
	if err != nil {
		t.Fatalf("kubernetes.New: %v", err)
	}
	return sources.NewProvider(
		cluster,
		loki.New("", nil),
		victoriametrics.New("", nil),
		argocd.New("", "", nil),
		kube,
	)
}

// countingDetector returns a fixed set of candidates and records its calls.
type countingDetector struct {
	cands []detector.Candidate
	calls int
}

func (d *countingDetector) Name() string { return "counting" }

func (d *countingDetector) Detect(_ context.Context, _ sources.Provider, _ detector.Scope) ([]detector.Candidate, error) {
	d.calls++
	return d.cands, nil
}

// ScanAndInvestigate must resolve the provider exactly once regardless of how
// many candidates detection yields, and return one ranked incident per
// candidate. This guards the per-tick "resolve + fetch once" optimization.
func TestScanAndInvestigateResolvesProviderOnce(t *testing.T) {
	var resolves int
	resolve := resolverFunc(func(cluster string) (sources.Provider, error) {
		resolves++
		return stubProvider(t, cluster), nil
	})

	now := time.Now()
	cands := []detector.Candidate{
		{Resource: model.ResourceRef{Cluster: "c", Namespace: "n", Kind: "Deployment", Name: "a"}, At: now},
		{Resource: model.ResourceRef{Cluster: "c", Namespace: "n", Kind: "Deployment", Name: "b"}, At: now},
		{Resource: model.ResourceRef{Cluster: "c", Namespace: "n", Kind: "Deployment", Name: "x"}, At: now},
	}
	det := &countingDetector{cands: cands}
	eng := New(resolve, slog.New(slog.NewTextHandler(io.Discard, nil)), det)

	incs, err := eng.ScanAndInvestigate(context.Background(), "c", detector.Scope{})
	if err != nil {
		t.Fatalf("ScanAndInvestigate: %v", err)
	}
	if resolves != 1 {
		t.Fatalf("expected provider resolved exactly once, got %d", resolves)
	}
	if det.calls != 1 {
		t.Fatalf("expected detector run once, got %d", det.calls)
	}
	if len(incs) != len(cands) {
		t.Fatalf("expected %d incidents, got %d", len(cands), len(incs))
	}
	for i, inc := range incs {
		if inc == nil || inc.ID == "" {
			t.Fatalf("incident %d is empty", i)
		}
		if inc.Resource != cands[i].Resource {
			t.Fatalf("incident %d resource = %v, want %v", i, inc.Resource, cands[i].Resource)
		}
	}
}

// A provider-resolution failure must surface as an error with no incidents,
// matching the old Scan behavior.
func TestScanAndInvestigateProviderError(t *testing.T) {
	wantErr := context.Canceled
	resolve := resolverFunc(func(string) (sources.Provider, error) { return nil, wantErr })
	eng := New(resolve, slog.New(slog.NewTextHandler(io.Discard, nil)), &countingDetector{})

	incs, err := eng.ScanAndInvestigate(context.Background(), "c", detector.Scope{})
	if err != wantErr {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if incs != nil {
		t.Fatalf("expected nil incidents on resolve error, got %v", incs)
	}
}
