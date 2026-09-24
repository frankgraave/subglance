package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQuietHoursValidate(t *testing.T) {
	ok := QuietHours{Start: "23:00", End: "07:00", Timezone: "Europe/Amsterdam", During: QuietHold}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
	for name, mutate := range map[string]func(*QuietHours){
		"bad start":      func(q *QuietHours) { q.Start = "7:00" },
		"bad end":        func(q *QuietHours) { q.End = "25:00" },
		"equal bounds":   func(q *QuietHours) { q.End = q.Start },
		"no timezone":    func(q *QuietHours) { q.Timezone = "" },
		"local timezone": func(q *QuietHours) { q.Timezone = "Local" },
		"unknown zone":   func(q *QuietHours) { q.Timezone = "Mars/Olympus" },
		"bad during":     func(q *QuietHours) { q.During = "snooze" },
	} {
		q := ok
		mutate(&q)
		if q.Validate() == nil {
			t.Errorf("%s: accepted %+v", name, q)
		}
	}
}

func TestQuietHoursActive(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	overnight := QuietHours{Start: "23:00", End: "07:00", Timezone: "Europe/Amsterdam", During: QuietHold}
	daytime := QuietHours{Start: "09:00", End: "17:30", Timezone: "Europe/Amsterdam", During: QuietHold}

	cases := []struct {
		q    QuietHours
		at   time.Time
		want bool
	}{
		{overnight, time.Date(2030, 1, 15, 22, 59, 0, 0, ams), false},
		{overnight, time.Date(2030, 1, 15, 23, 0, 0, 0, ams), true},
		{overnight, time.Date(2030, 1, 16, 3, 0, 0, 0, ams), true},
		{overnight, time.Date(2030, 1, 16, 6, 59, 0, 0, ams), true},
		{overnight, time.Date(2030, 1, 16, 7, 0, 0, 0, ams), false}, // end is exclusive
		{daytime, time.Date(2030, 1, 15, 8, 59, 0, 0, ams), false},
		{daytime, time.Date(2030, 1, 15, 12, 0, 0, 0, ams), true},
		{daytime, time.Date(2030, 1, 15, 17, 30, 0, 0, ams), false},
		// Evaluated in the window's zone, not the caller's: 23:30 UTC is
		// 00:30 in Amsterdam in winter.
		{overnight, time.Date(2030, 1, 15, 23, 30, 0, 0, time.UTC), true},
		{overnight, time.Date(2030, 1, 15, 21, 30, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		if got := c.q.Active(c.at); got != c.want {
			t.Errorf("%s–%s at %s: active=%v, want %v", c.q.Start, c.q.End, c.at, got, c.want)
		}
	}

	broken := overnight
	broken.Timezone = "Mars/Olympus"
	if broken.Active(time.Date(2030, 1, 16, 3, 0, 0, 0, ams)) {
		t.Error("an invalid window held an alert; quiet hours must fail open")
	}
}

// TestQuietHoursEndAfterFollowsTheWallClock: on the night the clocks go back,
// 07:00 is still 07:00, which is nine hours after 23:00 rather than eight.
func TestQuietHoursEndAfterFollowsTheWallClock(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	q := QuietHours{Start: "23:00", End: "07:00", Timezone: "Europe/Amsterdam", During: QuietHold}

	cases := []struct{ at, want time.Time }{
		{time.Date(2030, 1, 15, 23, 30, 0, 0, ams), time.Date(2030, 1, 16, 7, 0, 0, 0, ams)},
		{time.Date(2030, 1, 16, 2, 0, 0, 0, ams), time.Date(2030, 1, 16, 7, 0, 0, 0, ams)},
		// DST ends in Europe on 27 October 2030.
		{time.Date(2030, 10, 26, 23, 0, 0, 0, ams), time.Date(2030, 10, 27, 7, 0, 0, 0, ams)},
	}
	for _, c := range cases {
		if got := q.EndAfter(c.at); !got.Equal(c.want) {
			t.Errorf("EndAfter(%s) = %s, want %s", c.at, got, c.want)
		}
	}
	if got := q.EndAfter(cases[2].at).Sub(cases[2].at); got != 9*time.Hour {
		t.Errorf("the DST night lasted %s, want 9h", got)
	}
}

// TestQuietHoursEndAfterSkipsASpringForwardGap: 02:30 does not exist in New
// York on 10 March 2030. time.Date resolves it to 01:30 EST, which is still
// inside the window; the release has to fall after the gap instead.
func TestQuietHoursEndAfterSkipsASpringForwardGap(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	q := QuietHours{Start: "23:00", End: "02:30", Timezone: "America/New_York", During: QuietHold}

	at := time.Date(2030, 3, 10, 0, 0, 0, 0, ny)
	got := q.EndAfter(at)
	if want := time.Date(2030, 3, 10, 3, 0, 0, 0, ny); !got.Equal(want) {
		t.Errorf("EndAfter(%s) = %s, want %s", at, got, want)
	}
	if q.Active(got) {
		t.Errorf("the window is still active at its own end %s", got)
	}
}

func TestQuietHoursCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	ch, err := db.CreateChannel(ctx, Channel{Name: "phone", Type: ChannelWebhook,
		Config: map[string]string{"url": "https://example.com/hook"}, Enabled: true})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if _, ok, err := db.GetQuietHours(ctx, ch.ID); err != nil || ok {
		t.Fatalf("new channel has quiet hours: ok=%v err=%v", ok, err)
	}

	q := QuietHours{ChannelID: ch.ID, Start: "23:00", End: "07:00", Timezone: "Europe/Amsterdam", During: QuietHold}
	if err := db.SetQuietHours(ctx, q); err != nil {
		t.Fatalf("set: %v", err)
	}
	q.During = QuietDrop
	if err := db.SetQuietHours(ctx, q); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, ok, err := db.GetQuietHours(ctx, ch.ID)
	if err != nil || !ok || got != q {
		t.Fatalf("got %+v ok=%v err=%v, want %+v", got, ok, err, q)
	}
	all, err := db.ListQuietHours(ctx)
	if err != nil || len(all) != 1 || all[ch.ID] != q {
		t.Fatalf("list: %+v err=%v", all, err)
	}

	missing := q
	missing.ChannelID = ch.ID + 100
	if err := db.SetQuietHours(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("quiet hours for a missing channel: %v, want ErrNotFound", err)
	}

	if err := db.ClearQuietHours(ctx, ch.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := db.ClearQuietHours(ctx, ch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second clear: %v, want ErrNotFound", err)
	}

	// Deleting the channel takes its quiet hours with it.
	if err := db.SetQuietHours(ctx, q); err != nil {
		t.Fatalf("set again: %v", err)
	}
	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	if _, ok, err := db.GetQuietHours(ctx, ch.ID); err != nil || ok {
		t.Fatalf("quiet hours outlived their channel: ok=%v err=%v", ok, err)
	}
}

// TestQuietHoursEndAfterAcrossDSTEdges pins the two cases time.Date gets
// wrong: a gap where it resolves past the first valid instant, and a repeated
// hour where it picks the first occurrence and pushes the end a day out.
func TestQuietHoursEndAfterAcrossDSTEdges(t *testing.T) {
	syd, _ := time.LoadLocation("Australia/Sydney")
	ny, _ := time.LoadLocation("America/New_York")

	// Sydney springs forward from 02:00 to 03:00 on 4 October 2026, so 02:30
	// does not exist; the window ends at 03:00, the first valid instant.
	sydney := QuietHours{Start: "23:00", End: "02:30", Timezone: "Australia/Sydney", During: QuietHold}
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, syd)
	want := time.Date(2026, 10, 3, 16, 0, 0, 0, time.UTC) // 03:00 AEDT
	if got := sydney.EndAfter(at); !got.Equal(want) {
		t.Errorf("Sydney gap: EndAfter(%s) = %s, want %s", at, got.In(syd), want.In(syd))
	}

	// New York repeats 01:00-02:00 on 1 November 2026. From the second 01:15
	// (EST) the end 01:30 comes round again in fifteen minutes.
	newYork := QuietHours{Start: "23:00", End: "01:30", Timezone: "America/New_York", During: QuietHold}
	secondQuarterPast := time.Date(2026, 11, 1, 6, 15, 0, 0, time.UTC) // 01:15 EST
	if got := newYork.EndAfter(secondQuarterPast); !got.Equal(secondQuarterPast.Add(15 * time.Minute)) {
		t.Errorf("New York repeated hour: EndAfter(%s) = %s, want 15 minutes later",
			secondQuarterPast.In(ny), got.In(ny))
	}
	firstQuarterPast := time.Date(2026, 11, 1, 5, 15, 0, 0, time.UTC) // 01:15 EDT
	if got := newYork.EndAfter(firstQuarterPast); !got.Equal(firstQuarterPast.Add(15 * time.Minute)) {
		t.Errorf("New York first pass: EndAfter(%s) = %s, want 15 minutes later",
			firstQuarterPast.In(ny), got.In(ny))
	}
}
