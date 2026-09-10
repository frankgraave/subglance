// Package events is the in-process fan-out between the checker pipeline and
// anything that wants to watch it live.
//
// It exists so the runner does not need to know who is listening. The runner
// publishes; the SSE endpoint, and later the notifier, subscribe.
//
// Two rules shape the whole design:
//
//  1. A slow subscriber must never slow down monitoring. Every subscriber gets
//     a buffered channel and a subscriber that cannot keep up loses events
//     rather than blocking the publisher. A browser tab on a bad connection is
//     not allowed to stall the scheduler.
//
//  2. Dropping is visible, not silent. A dropped event increments a counter the
//     subscriber can read, so the UI can tell the difference between "nothing
//     happened" and "you missed something" — which is exactly the distinction
//     this product exists to make.
package events

import (
	"sync"
	"sync/atomic"
	"time"
)

// Kind identifies what happened.
type Kind string

const (
	// KindHeartbeat is one completed check. This is the high-volume event:
	// one per monitor per interval.
	KindHeartbeat Kind = "heartbeat"

	// KindStatus is a monitor changing state (up/down/degraded/paused).
	KindStatus Kind = "status"

	// KindIncident is an incident opening, being confirmed, acknowledged or
	// resolved.
	KindIncident Kind = "incident"
)

// Event is one thing that happened, ready to be serialised to a client.
//
// Payload is deliberately an any: the bus does not care about the shape, and
// keeping the API layer responsible for serialisation avoids a dependency
// cycle between events and store.
type Event struct {
	Kind      Kind      `json:"kind"`
	MonitorID int64     `json:"monitor_id,omitempty"`
	At        time.Time `json:"at"`
	Payload   any       `json:"data,omitempty"`

	// Seq is assigned by the bus. Clients send it back as Last-Event-ID on
	// reconnect so they can tell whether they missed anything.
	Seq uint64 `json:"seq"`
}

// Subscription is one listener's view of the bus.
type Subscription struct {
	ch      chan Event
	bus     *Bus
	dropped atomic.Uint64
	once    sync.Once
}

// C returns the channel events arrive on. It is closed when the subscription
// is cancelled.
func (s *Subscription) C() <-chan Event { return s.ch }

// Dropped reports how many events this subscriber missed because it could not
// keep up. Non-zero means the client's view has a hole in it and it should
// re-fetch rather than assume continuity.
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }

// Close removes the subscription. Safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.bus.remove(s)
		close(s.ch)
	})
}

// Bus fans events out to every current subscriber.
//
// The zero value is not usable; call NewBus.
type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscription]struct{}
	seq  atomic.Uint64

	// buffer is how many events a subscriber may fall behind before it
	// starts losing them.
	buffer int
}

// NewBus returns an empty bus.
//
// buffer sets the per-subscriber queue depth. It wants to be large enough to
// absorb a burst — every monitor reporting at once after a restart — but small
// enough that a dead tab does not pin megabytes. 256 covers a 200-monitor
// install reporting simultaneously.
func NewBus(buffer int) *Bus {
	if buffer <= 0 {
		buffer = 256
	}
	return &Bus{
		subs:   make(map[*Subscription]struct{}),
		buffer: buffer,
	}
}

// Subscribe registers a new listener.
//
// The caller must Close the subscription when done, otherwise the bus keeps
// publishing into a channel nobody reads.
func (b *Bus) Subscribe() *Subscription {
	s := &Subscription{
		ch:  make(chan Event, b.buffer),
		bus: b,
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (b *Bus) remove(s *Subscription) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// Subscribers reports how many listeners are currently attached. Used by tests
// and by the readiness endpoint.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Publish sends an event to every subscriber.
//
// It never blocks. A subscriber whose buffer is full loses this event and has
// its dropped counter incremented — the publisher is the monitoring pipeline
// and must not be held up by anyone's slow socket.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.Seq = b.seq.Add(1)

	b.mu.RLock()
	defer b.mu.RUnlock()

	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

// LastSeq returns the sequence number of the most recent event. A client that
// reconnects with a lower Last-Event-ID knows it missed something.
func (b *Bus) LastSeq() uint64 { return b.seq.Load() }
