package events

import (
	"sync"
	"testing"
	"time"

	"lotsman/internal/model"
)

func inc(id string) *model.Incident { return &model.Incident{ID: id} }

func recv(t *testing.T, ch <-chan *model.Incident) *model.Incident {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for incident")
		return nil
	}
}

func TestSubscribePublishDelivers(t *testing.T) {
	b := NewIncidentBus()
	ch, cancel := b.Subscribe()
	defer cancel()

	want := inc("a")
	b.Publish(want)
	if got := recv(t, ch); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := NewIncidentBus()
	ch1, c1 := b.Subscribe()
	ch2, c2 := b.Subscribe()
	defer c1()
	defer c2()

	want := inc("b")
	b.Publish(want)
	if got := recv(t, ch1); got != want {
		t.Fatalf("sub1 got %v, want %v", got, want)
	}
	if got := recv(t, ch2); got != want {
		t.Fatalf("sub2 got %v, want %v", got, want)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewIncidentBus()
	ch, cancel := b.Subscribe()

	cancel()
	if b.SubscriberCount() != 0 {
		t.Fatalf("expected 0 subscribers after cancel, got %d", b.SubscriberCount())
	}
	// Channel must be closed so a ranging consumer unblocks.
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after cancel")
	}
	// Publishing after cancel must not panic.
	b.Publish(inc("c"))
	// cancel is idempotent.
	cancel()
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	b := NewIncidentBus()
	_, cancel := b.Subscribe() // never drained
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < subBuffer*4; i++ {
			b.Publish(inc("x"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a full/slow subscriber")
	}
}

func TestConcurrentSubscribePublish(t *testing.T) {
	b := NewIncidentBus()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := b.Subscribe()
			defer cancel()
			b.Publish(inc("y"))
			select {
			case <-ch:
			case <-time.After(time.Second):
			}
		}()
	}
	wg.Wait()
}
