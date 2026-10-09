package checker

import (
	"context"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// lossyConn stands in for an ICMP socket on a network that loses packets on
// purpose. respond is called for every echo request the check sends, with
// its 0-based position and sequence number, and returns the packet that
// comes back (nil for none) and how long it takes to arrive.
type lossyConn struct {
	t       *testing.T
	peer    net.Addr
	respond func(n, seq int, sent []byte) (reply []byte, after time.Duration)

	mu       sync.Mutex
	sends    []time.Time
	inbox    []delivery
	deadline time.Time
}

type delivery struct {
	at  time.Time
	pkt []byte
}

func (c *lossyConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	msg, err := icmp.ParseMessage(1, b)
	if err != nil {
		c.t.Fatalf("the check sent something that is not ICMP: %v", err)
	}
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok || msg.Type != ipv4.ICMPTypeEcho {
		c.t.Fatalf("the check sent %v, want an echo request", msg.Type)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if reply, after := c.respond(len(c.sends), echo.Seq, b); reply != nil {
		c.inbox = append(c.inbox, delivery{at: now.Add(after), pkt: reply})
	}
	c.sends = append(c.sends, now)
	return len(b), nil
}

func (c *lossyConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		now := time.Now()
		for i, d := range c.inbox {
			if !d.at.After(now) {
				c.inbox = append(c.inbox[:i], c.inbox[i+1:]...)
				c.mu.Unlock()
				return copy(b, d.pkt), c.peer, nil
			}
		}
		expired := !c.deadline.IsZero() && !now.Before(c.deadline)
		c.mu.Unlock()
		if expired {
			return 0, nil, &net.OpError{Op: "read", Net: "udp4", Err: os.ErrDeadlineExceeded}
		}
		time.Sleep(time.Millisecond)
	}
}

func (c *lossyConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return nil
}

func (c *lossyConn) SetWriteDeadline(time.Time) error { return nil }

func (c *lossyConn) sent() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.sends...)
}

var lossyTarget = netip.MustParseAddr("192.0.2.10")

func echoReply(t *testing.T, seq int) []byte {
	t.Helper()
	b, err := (&icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Body: &icmp.Echo{ID: 1, Seq: seq, Data: []byte("SubGlance")},
	}).Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runLossy runs one check against a lossyConn the way Check does after the
// socket is open, on an unprivileged socket (the kernel owns the echo ID).
func runLossy(t *testing.T, every, timeout time.Duration,
	respond func(n, seq int, sent []byte) ([]byte, time.Duration),
) (Result, *lossyConn) {
	t.Helper()
	c := NewPingChecker(NewGuard(true))
	c.mode = pingModeUnprivileged
	c.every = every
	conn := &lossyConn{t: t, peer: &net.UDPAddr{IP: lossyTarget.AsSlice()}, respond: respond}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.exchange(ctx, conn, lossyTarget, start), conn
}

// One dropped echo request is routine on the internet. It must cost a
// second, not a failed check: with retries at 0, as an Uptime Kuma import
// sets for Kuma's default, a failed check is an alert.
func TestPingSendsAgainAfterALostRequest(t *testing.T) {
	res, conn := runLossy(t, 200*time.Millisecond, 5*time.Second, func(n, seq int, _ []byte) ([]byte, time.Duration) {
		if n == 0 {
			return nil, 0 // the first request is lost
		}
		return echoReply(t, seq), 0
	})
	if !res.OK {
		t.Fatalf("a lost first request failed the check: %s: %s", res.Kind, res.Error)
	}
	sends := conn.sent()
	if len(sends) != 2 {
		t.Fatalf("sent %d requests, want 2 (one lost, one answered)", len(sends))
	}
	// Measured from the request that was answered: the second the lost
	// packet cost is not a slower network.
	if gap := sends[1].Sub(sends[0]); res.Latency >= gap {
		t.Errorf("latency %s includes the %s spent waiting on the lost request", res.Latency, gap)
	}
}

// A reply to an earlier request that arrives after the next one went out is
// still an answer from the target.
func TestPingCountsALateReplyToAnEarlierRequest(t *testing.T) {
	every := 200 * time.Millisecond
	res, conn := runLossy(t, every, 5*time.Second, func(n, seq int, _ []byte) ([]byte, time.Duration) {
		if n == 0 {
			return echoReply(t, seq), every + every/2
		}
		return nil, 0
	})
	if !res.OK {
		t.Fatalf("a late reply to the first request was not counted: %s: %s", res.Kind, res.Error)
	}
	if n := len(conn.sent()); n != 2 {
		t.Fatalf("sent %d requests, want 2", n)
	}
	if res.Latency < every {
		t.Errorf("latency %s, want it measured from the first request (at least %s)", res.Latency, every)
	}
}

// A reply carrying a sequence number this check has not sent is someone
// else's, or a forgery; it must not pass the check.
func TestPingIgnoresRepliesToRequestsNotSent(t *testing.T) {
	res, conn := runLossy(t, 100*time.Millisecond, 700*time.Millisecond, func(_, seq int, _ []byte) ([]byte, time.Duration) {
		return echoReply(t, seq+1), 0 // the next number, not yet sent
	})
	if res.OK {
		t.Fatal("a reply to a request that was never sent passed the check")
	}
	if res.Kind != FailTimeout {
		t.Errorf("kind = %q, want %q", res.Kind, FailTimeout)
	}
	if len(conn.sent()) < 2 {
		t.Errorf("sent %d requests, want the check to keep trying until its timeout", len(conn.sent()))
	}
}

// A target that never answers fails as a timeout at the deadline, having
// sent no more than one request per interval: a down host is not flooded.
func TestPingTimesOutAfterOneRequestPerInterval(t *testing.T) {
	every, timeout := 200*time.Millisecond, time.Second
	start := time.Now()
	res, conn := runLossy(t, every, timeout, func(int, int, []byte) ([]byte, time.Duration) { return nil, 0 })
	elapsed := time.Since(start)

	if res.OK {
		t.Fatal("a check with no reply passed")
	}
	if res.Kind != FailTimeout || !strings.Contains(res.Error, "timed out") {
		t.Errorf("got %s: %q, want a timeout", res.Kind, res.Error)
	}
	// The lower bound is exact: the check may not give up early. The upper
	// bound only has to catch a check that runs on past its deadline, so it
	// is a whole timeout wide; a loaded CI host under -race can be hundreds
	// of milliseconds late waking the reader, and that is not a defect.
	if elapsed < timeout || elapsed > 2*timeout {
		t.Errorf("check took %s, want it to end at its %s timeout", elapsed, timeout)
	}
	sends := conn.sent()
	if most := int(timeout/every) + 1; len(sends) < 2 || len(sends) > most {
		t.Errorf("sent %d requests in %s, want between 2 and %d", len(sends), timeout, most)
	}
	for i := 1; i < len(sends); i++ {
		if gap := sends[i].Sub(sends[i-1]); gap < every*9/10 {
			t.Errorf("request %d followed the previous one after %s, want at least %s", i+1, gap, every)
		}
	}
}

// "Destination unreachable" about any of this check's requests is the
// network answering, not a lost packet: the check fails at once.
func TestPingFailsOnAnErrorAboutALaterRequest(t *testing.T) {
	every, timeout := 100*time.Millisecond, 5*time.Second
	start := time.Now()
	res, _ := runLossy(t, every, timeout, func(n, _ int, sent []byte) ([]byte, time.Duration) {
		if n == 0 {
			return nil, 0
		}
		quote := make([]byte, 20, 28)
		quote[0] = 0x45
		copy(quote[16:20], lossyTarget.AsSlice())
		quote = append(quote, sent[:8]...)
		b, err := (&icmp.Message{
			Type: ipv4.ICMPTypeDestinationUnreachable, Code: 1,
			Body: &icmp.DstUnreach{Data: quote},
		}).Marshal(nil)
		if err != nil {
			t.Fatal(err)
		}
		return b, 0
	})
	if res.OK {
		t.Fatal("an unreachable error about the second request passed the check")
	}
	if !strings.Contains(res.Error, "destination unreachable") {
		t.Errorf("error = %q, want it to say the destination is unreachable", res.Error)
	}
	if elapsed := time.Since(start); elapsed > timeout/2 {
		t.Errorf("check took %s, want it to fail when the error arrived", elapsed)
	}
}
