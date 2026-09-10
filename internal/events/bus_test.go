package events

import (
	"sync"
	"testing"
	"time"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	b := NewBus(8)
	a, c := b.Subscribe(), b.Subscribe()
	defer a.Close()
	defer c.Close()

	b.Publish(Event{Kind: KindHeartbeat, MonitorID: 1})

	for i, s := range []*Subscription{a, c} {
		select {
		case e := <-s.C():
			if e.Kind != KindHeartbeat || e.MonitorID != 1 {
				t.Fatalf("subscriber %d got %+v", i, e)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d received nothing", i)
		}
	}
}

func TestPublishStampsSequenceAndTime(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe()
	defer s.Close()

	b.Publish(Event{Kind: KindStatus})
	b.Publish(Event{Kind: KindStatus})

	first, second := <-s.C(), <-s.C()
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("sequence not monotonic: %d then %d", first.Seq, second.Seq)
	}
	if first.At.IsZero() {
		t.Fatal("timestamp not stamped")
	}
	if b.LastSeq() != 2 {
		t.Fatalf("LastSeq = %d, want 2", b.LastSeq())
	}
}

// A subscriber that stops reading must lose events rather than block the
// publisher. This is the property the whole design exists for: a browser tab on
// a bad connection cannot be allowed to stall the checker pipeline.
func TestSlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := NewBus(2)
	s := b.Subscribe()
	defer s.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			b.Publish(Event{Kind: KindHeartbeat, MonitorID: int64(i)})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}

	if s.Dropped() == 0 {
		t.Fatal("expected dropped events to be counted, got 0")
	}
	if got := len(s.C()); got != 2 {
		t.Fatalf("buffer holds %d events, want 2", got)
	}
}

func TestCloseRemovesSubscriber(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe()

	if b.Subscribers() != 1 {
		t.Fatalf("Subscribers = %d, want 1", b.Subscribers())
	}

	s.Close()

	if b.Subscribers() != 0 {
		t.Fatalf("Subscribers = %d after close, want 0", b.Subscribers())
	}
	if _, open := <-s.C(); open {
		t.Fatal("channel still open after Close")
	}

	// Publishing to a bus with no subscribers must not panic.
	b.Publish(Event{Kind: KindStatus})
}

func TestCloseIsIdempotent(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe()

	s.Close()
	s.Close() // must not panic on a double close of the channel
}

// Subscribing and closing while events are in flight must not race. Run with
// -race for this to mean anything.
func TestConcurrentSubscribeAndPublish(t *testing.T) {
	b := NewBus(16)

	stop := make(chan struct{})
	publisherDone := make(chan struct{})

	go func() {
		defer close(publisherDone)
		for {
			select {
			case <-stop:
				return
			default:
				b.Publish(Event{Kind: KindHeartbeat})
				// Yield between publishes. A tight RLock loop can starve
				// Subscribe's write lock, and a publisher with no gap
				// between events is not a real workload anyway.
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Only the subscribers are waited on here. Adding the publisher to this
	// group would deadlock: it exits on close(stop), which cannot run until
	// Wait returns.
	var subs sync.WaitGroup
	for i := 0; i < 20; i++ {
		subs.Add(1)
		go func() {
			defer subs.Done()
			s := b.Subscribe()
			defer s.Close()
			select {
			case <-s.C():
			case <-time.After(5 * time.Second):
				t.Error("subscriber received nothing within 5s")
			}
		}()
	}
	subs.Wait()

	close(stop)
	<-publisherDone

	if b.Subscribers() != 0 {
		t.Fatalf("leaked %d subscribers", b.Subscribers())
	}
}
