package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frankgraave/subglance/internal/events"
)

// readFrame reads one SSE frame (everything up to a blank line).
func readFrame(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading frame: %v (got %q so far)", err, sb.String())
		}
		if line == "\n" {
			return sb.String()
		}
		sb.WriteString(line)
	}
}

func frameField(frame, prefix string) string {
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// openStream connects to the live endpoint and consumes the two opening
// frames, leaving the reader positioned at the first real event.
func openStream(t *testing.T, base string, h http.Handler) (*bufio.Reader, func()) {
	t.Helper()

	req, err := http.NewRequest("GET", base+"/api/v1/stream", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	resp, err := http.DefaultClient.Do(req.WithContext(ctx)) //nolint:bodyclose // closed by the returned func
	if err != nil {
		cancel()
		t.Fatalf("opening stream: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		cancel()
		resp.Body.Close()
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	br := bufio.NewReader(resp.Body)
	if got := readFrame(t, br); !strings.Contains(got, "retry:") {
		t.Fatalf("first frame = %q, want a retry hint", got)
	}
	if got := readFrame(t, br); !strings.Contains(got, "event: hello") {
		t.Fatalf("second frame = %q, want hello", got)
	}

	return br, func() {
		cancel()
		resp.Body.Close()
	}
}

// streamServer boots an authenticated test server with a bus attached.
func streamServer(t *testing.T) (*events.Bus, *httptest.Server) {
	t.Helper()
	return streamServerPinging(t, 0)
}

// streamServerPinging is streamServer with an explicit keepalive interval, so
// a test can observe a ping without waiting out the twenty-second default.
func streamServerPinging(t *testing.T, ping time.Duration) (*events.Bus, *httptest.Server) {
	t.Helper()

	srv, _ := testServerWithDB(t)
	bus := events.NewBus(16)
	srv.WithBus(bus)
	srv.streamPing = ping

	ts := httptest.NewServer(authedHandler(srv))
	t.Cleanup(ts.Close)
	return bus, ts
}

func TestStreamSetsStreamingHeaders(t *testing.T) {
	_, ts := streamServer(t)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/stream", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	// Without this nginx buffers the response and a live stream looks dead.
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

func TestStreamDeliversHeartbeat(t *testing.T) {
	bus, ts := streamServer(t)
	br, done := openStream(t, ts.URL, nil)
	defer done()

	waitForSubscribers(t, bus, 1)
	bus.Publish(events.Event{
		Kind:      events.KindHeartbeat,
		MonitorID: 7,
		Payload:   map[string]any{"ok": true, "latency_ms": 42},
	})

	frame := readFrame(t, br)

	if ev := frameField(frame, "event: "); ev != "heartbeat" {
		t.Fatalf("event = %q, want heartbeat", ev)
	}
	if id := frameField(frame, "id: "); id == "" {
		t.Error("no id line; clients cannot resume with Last-Event-ID")
	}

	var got events.Event
	if err := json.Unmarshal([]byte(frameField(frame, "data: ")), &got); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if got.MonitorID != 7 {
		t.Errorf("monitor_id = %d, want 7", got.MonitorID)
	}
	if got.Seq == 0 {
		t.Error("seq not assigned")
	}
}

func TestStreamDeliversStatusChange(t *testing.T) {
	bus, ts := streamServer(t)
	br, done := openStream(t, ts.URL, nil)
	defer done()

	waitForSubscribers(t, bus, 1)
	bus.Publish(events.Event{
		Kind:      events.KindStatus,
		MonitorID: 3,
		Payload:   map[string]any{"event": "incident_confirmed"},
	})

	frame := readFrame(t, br)
	if ev := frameField(frame, "event: "); ev != "status" {
		t.Fatalf("event = %q, want status", ev)
	}
}

// Every client must see every event: this is a broadcast, not a queue.
func TestStreamFansOutToMultipleClients(t *testing.T) {
	bus, ts := streamServer(t)

	readers := make([]*bufio.Reader, 0, 3)
	for i := 0; i < 3; i++ {
		br, done := openStream(t, ts.URL, nil)
		defer done()
		readers = append(readers, br)
	}

	waitForSubscribers(t, bus, 3)
	bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 99})

	for i, br := range readers {
		frame := readFrame(t, br)
		var got events.Event
		if err := json.Unmarshal([]byte(frameField(frame, "data: ")), &got); err != nil {
			t.Fatalf("client %d: bad JSON: %v", i, err)
		}
		if got.MonitorID != 99 {
			t.Errorf("client %d: monitor_id = %d, want 99", i, got.MonitorID)
		}
	}
}

// Disconnecting must release the subscription, or a long-running server leaks
// a goroutine and a buffer per browser tab that was ever opened.
func TestStreamUnsubscribesOnDisconnect(t *testing.T) {
	bus, ts := streamServer(t)

	_, done := openStream(t, ts.URL, nil)
	waitForSubscribers(t, bus, 1)

	done()

	deadline := time.Now().Add(3 * time.Second)
	for bus.Subscribers() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if bus.Subscribers() != 0 {
		t.Fatalf("subscription leaked: %d still attached", bus.Subscribers())
	}
}

// The stream carries every monitor name and outage in the install. It must not
// be readable without credentials.
func TestStreamRequiresAuth(t *testing.T) {
	srv, _ := testServerWithDB(t)
	srv.WithBus(events.NewBus(16))

	// Deliberately the raw handler: no token is injected.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/stream")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// Without a bus the endpoint must fail honestly rather than panic.
func TestStreamWithoutBusIsUnavailable(t *testing.T) {
	srv, _ := testServerWithDB(t)
	ts := httptest.NewServer(authedHandler(srv))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/stream")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// waitForSubscribers blocks until the handler has registered, so publishing
// does not race the subscription.
func waitForSubscribers(t *testing.T, bus *events.Bus, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for bus.Subscribers() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := bus.Subscribers(); got != want {
		t.Fatalf("subscribers = %d, want %d", got, want)
	}
}

// Reconnecting is the half of SUB-17's acceptance that unit tests usually miss.
// A client that drops and comes back must be told whether it missed anything,
// because "no events" and "events you never saw" look identical otherwise —
// and this product exists to make exactly that distinction.
func TestStreamReportsGapOnReconnect(t *testing.T) {
	bus, ts := streamServer(t)

	// First connection sees one event, then goes away.
	br, done := openStream(t, ts.URL, nil)
	waitForSubscribers(t, bus, 1)
	bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 1})

	frame := readFrame(t, br)
	lastID := frameField(frame, "id: ")
	if lastID == "" {
		t.Fatal("no id on the first event; a client has nothing to resume from")
	}
	done()

	// While disconnected, the world moves on.
	for i := 0; i < 3; i++ {
		bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 2})
	}

	// Reconnect, announcing where we left off.
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/stream", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Last-Event-ID", lastID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("reconnecting: %v", err)
	}
	defer resp.Body.Close()

	br2 := bufio.NewReader(resp.Body)
	readFrame(t, br2) // retry hint

	var hello helloPayload
	if err := json.Unmarshal([]byte(frameField(readFrame(t, br2), "data: ")), &hello); err != nil {
		t.Fatalf("hello is not valid JSON: %v", err)
	}

	if !hello.Gap {
		t.Error("gap = false after missing three events; the client would " +
			"assume its view is current when it has a hole in it")
	}
	if hello.Missed != 3 {
		t.Errorf("missed = %d, want 3", hello.Missed)
	}
}

// A client that reconnects having missed nothing must not be told it did, or
// the UI cries wolf on every routine reconnect and people learn to ignore it.
func TestStreamNoGapWhenCaughtUp(t *testing.T) {
	bus, ts := streamServer(t)

	br, done := openStream(t, ts.URL, nil)
	waitForSubscribers(t, bus, 1)
	bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 1})
	lastID := frameField(readFrame(t, br), "id: ")
	done()

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/stream", nil)
	req.Header.Set("Last-Event-ID", lastID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("reconnecting: %v", err)
	}
	defer resp.Body.Close()

	br2 := bufio.NewReader(resp.Body)
	readFrame(t, br2)

	var hello helloPayload
	if err := json.Unmarshal([]byte(frameField(readFrame(t, br2), "data: ")), &hello); err != nil {
		t.Fatalf("hello is not valid JSON: %v", err)
	}
	if hello.Gap {
		t.Error("gap reported although the client had seen everything")
	}
}

// Browsers cannot set headers on an EventSource, so the query fallback is the
// only resume path the actual frontend can use. If it silently did nothing,
// reconnect would be broken in exactly the client that matters.
func TestStreamAcceptsLastEventIDViaQuery(t *testing.T) {
	bus, ts := streamServer(t)

	bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 1})
	bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET",
		ts.URL+"/api/v1/stream?last_event_id=1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	readFrame(t, br)

	var hello helloPayload
	if err := json.Unmarshal([]byte(frameField(readFrame(t, br), "data: ")), &hello); err != nil {
		t.Fatalf("hello is not valid JSON: %v", err)
	}
	if !hello.Gap || hello.Missed != 1 {
		t.Errorf("gap = %v, missed = %d; want a gap of 1 from the query parameter",
			hello.Gap, hello.Missed)
	}
}

// A slow client must lose events rather than stall the monitoring pipeline —
// and must be told, so it can re-fetch instead of trusting a partial picture.
func TestStreamTellsClientItLagged(t *testing.T) {
	bus := events.NewBus(2)
	srv, _ := testServerWithDB(t)
	srv.WithBus(bus)
	ts := httptest.NewServer(authedHandler(srv))
	defer ts.Close()

	br, done := openStream(t, ts.URL, nil)
	defer done()
	waitForSubscribers(t, bus, 1)

	// Overrun the buffer without reading. Publish must not block.
	publishDone := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			bus.Publish(events.Event{Kind: events.KindHeartbeat, MonitorID: 5})
		}
		close(publishDone)
	}()

	select {
	case <-publishDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber: a stuck browser tab " +
			"must never be able to stall the checker pipeline")
	}

	// Somewhere in the stream the client is told it fell behind.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(readFrame(t, br), "event: lagged") {
			return
		}
	}
	t.Fatal("client was never told it had lagged; it would present an " +
		"incomplete picture as if it were complete")
}

// The keepalive must be an event the browser can see, not an SSE comment.
//
// A `: ping` comment is dropped by EventSource before any listener runs, so a
// page cannot tell an idle stream from a socket that died silently — which is
// exactly the failure mode DESIGN.md §6 says the client watchdog must catch.
// It also carries no id, so it must not move a reconnecting client's resume
// point.
func TestStreamPingIsAnObservableEvent(t *testing.T) {
	_, ts := streamServerPinging(t, 20*time.Millisecond)

	br, closeStream := openStream(t, ts.URL, nil)
	defer closeStream()

	frame := readFrame(t, br)
	if got := frameField(frame, "event:"); got != "ping" {
		t.Fatalf("first idle frame = %q, want an event named ping", frame)
	}
	if strings.Contains(frame, "id:") {
		t.Errorf("ping frame = %q, want no id line: it must not move Last-Event-ID", frame)
	}

	var body struct {
		ServerT time.Time `json:"server_time"`
	}
	if err := json.Unmarshal([]byte(frameField(frame, "data:")), &body); err != nil {
		t.Fatalf("decoding ping payload: %v", err)
	}
	if body.ServerT.IsZero() {
		t.Error("ping payload has no server_time; a client cannot date the silence")
	}
}

// The client sizes its silence watchdog from what the server promises, so the
// promise has to be on the wire.
func TestStreamHelloAnnouncesPingInterval(t *testing.T) {
	_, ts := streamServerPinging(t, 250*time.Millisecond)

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/stream", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	readFrame(t, br) // retry hint
	hello := readFrame(t, br)

	var body struct {
		PingMS int64 `json:"ping_interval_ms"`
	}
	if err := json.Unmarshal([]byte(frameField(hello, "data:")), &body); err != nil {
		t.Fatalf("decoding hello: %v", err)
	}
	if body.PingMS != 250 {
		t.Errorf("hello ping_interval_ms = %d, want 250", body.PingMS)
	}
}
