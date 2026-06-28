package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"lotsman/internal/model"
)

// TestMemoryConcurrency verifies that Memory is race-free under concurrent
// SaveIncident, ListIncidents, and GetIncident calls.
//
// Run: go test ./internal/store -race -run TestMemoryConcurrency
func TestMemoryConcurrency(t *testing.T) {
	mem := NewMemory()
	ctx := context.Background()
	now := time.Now()

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// 20 writers: each saves a uniquely-IDed incident.
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			inc := &model.Incident{
				ID:       fmt.Sprintf("inc-%d", i),
				Resource: model.ResourceRef{Cluster: "c", Namespace: "ns", Kind: "Deployment", Name: fmt.Sprintf("svc-%d", i)},
				Status:   model.IncidentOpen,
				OpenedAt: now.Add(-time.Duration(i) * time.Second),
			}
			if err := mem.SaveIncident(ctx, inc); err != nil {
				t.Errorf("SaveIncident(%d): %v", i, err)
			}
		}()
	}

	// 20 listers: call ListIncidents in a loop for ~100ms.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(100 * time.Millisecond)
			for time.Now().Before(deadline) {
				if _, err := mem.ListIncidents(ctx, IncidentFilter{}); err != nil {
					t.Errorf("ListIncidents: %v", err)
					return
				}
			}
		}()
	}

	// 20 getters: try to fetch each incident (may hit ErrNotFound before it is
	// written, which is fine — we only check for unexpected errors).
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(100 * time.Millisecond)
			id := fmt.Sprintf("inc-%d", i)
			for time.Now().Before(deadline) {
				_, err := mem.GetIncident(ctx, id)
				if err != nil && err != ErrNotFound {
					t.Errorf("GetIncident(%s): unexpected error: %v", id, err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// After all writers are done, every incident must be retrievable.
	for i := 0; i < goroutines; i++ {
		id := fmt.Sprintf("inc-%d", i)
		if _, err := mem.GetIncident(ctx, id); err != nil {
			t.Errorf("post-concurrency GetIncident(%s): %v", id, err)
		}
	}
}
