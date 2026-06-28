package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"lotsman/internal/model"
	"lotsman/internal/sources"
	"lotsman/internal/sources/argocd"
	"lotsman/internal/sources/kubernetes"
	"lotsman/internal/sources/loki"
	"lotsman/internal/sources/victoriametrics"
)

type resolverFunc func(string) (sources.Provider, error)

func (f resolverFunc) Provider(cluster string) (sources.Provider, error) { return f(cluster) }

// Investigate must succeed even when every source is unimplemented (returns
// ErrNotImplemented): the correlator tolerates per-source failure and the engine
// still produces an incident. This guards the graceful-degradation contract.
func TestInvestigateToleratesUnimplementedSources(t *testing.T) {
	resolve := resolverFunc(func(cluster string) (sources.Provider, error) {
		kube, err := kubernetes.New(cluster, "")
		if err != nil {
			return nil, err
		}
		return sources.NewProvider(
			cluster,
			loki.New("", nil),
			victoriametrics.New("", nil),
			argocd.New("", "", nil),
			kube,
		), nil
	})

	eng := New(resolve, slog.New(slog.NewTextHandler(io.Discard, nil)))
	inc, err := eng.Investigate(
		context.Background(),
		model.ResourceRef{Cluster: "c", Namespace: "n", Kind: "Deployment", Name: "x"},
		time.Now(), time.Hour,
	)
	if err != nil {
		t.Fatalf("Investigate should not fail when sources are stubbed: %v", err)
	}
	if inc == nil || inc.ID == "" {
		t.Fatal("expected a non-empty incident")
	}
}
