// Package events provides an in-process publish/subscribe bus for incidents.
// It decouples producers (the detector scheduler, manual investigations) from
// consumers (SSE clients) without either side importing the other. It depends
// only on internal/model so both internal/api and internal/controlplane can use
// it free of import cycles.
package events

import (
	"sync"

	"lotsman/internal/model"
)

// subBuffer is how many incidents a subscriber may fall behind before further
// publishes to it are dropped. A slow SSE client must never stall detection.
const subBuffer = 16

// subscriber is one fan-out destination. Its own mutex guards the channel's
// lifecycle so Publish can send without holding the bus-wide lock: a send takes
// the subscriber's lock and skips a channel already closed by cancel, which
// makes sending after close safe (no panic) without serializing publishers on a
// single global lock.
type subscriber struct {
	mu     sync.Mutex
	ch     chan *model.Incident
	closed bool
}

// send delivers inc to this subscriber without blocking, dropping if the buffer
// is full or the subscriber has been cancelled.
func (s *subscriber) send(inc *model.Incident) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- inc:
	default:
		// Subscriber is behind; drop to keep detection non-blocking.
	}
}

// IncidentBus is a concurrency-safe fan-out bus for *model.Incident values.
// Publish never blocks: a subscriber whose buffer is full simply misses the
// update. The zero value is not usable; call NewIncidentBus.
type IncidentBus struct {
	mu   sync.Mutex
	next int
	subs map[int]*subscriber
}

// NewIncidentBus constructs an empty bus.
func NewIncidentBus() *IncidentBus {
	return &IncidentBus{subs: make(map[int]*subscriber)}
}

// Subscribe returns a channel of incidents and a cancel function. The caller
// owns neither the channel's closing nor its lifetime beyond calling cancel:
// cancel removes the subscription and closes the channel exactly once, so a
// ranging consumer unblocks. cancel is safe to call multiple times.
func (b *IncidentBus) Subscribe() (<-chan *model.Incident, func()) {
	sub := &subscriber{ch: make(chan *model.Incident, subBuffer)}

	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = sub
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			// Close under the subscriber's own lock so a concurrent send observes
			// closed and skips, rather than panicking on a send-after-close.
			sub.mu.Lock()
			sub.closed = true
			close(sub.ch)
			sub.mu.Unlock()
		})
	}
	return sub.ch, cancel
}

// Publish delivers inc to every current subscriber without blocking. Subscribers
// whose buffer is full drop the update rather than back-pressure the publisher.
// The subscriber set is snapshotted under the bus lock, which is then released
// before any sends, so a slow or blocked subscriber never stalls other
// publishers.
func (b *IncidentBus) Publish(inc *model.Incident) {
	if inc == nil {
		return
	}
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()

	for _, sub := range subs {
		sub.send(inc)
	}
}

// SubscriberCount reports the number of active subscribers (used in tests).
func (b *IncidentBus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
