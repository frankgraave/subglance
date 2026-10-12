package store

import (
	"errors"
	"testing"
)

func TestNormaliseQuietHoursTrimsAndDefaultsToHold(t *testing.T) {
	got, err := NormaliseQuietHours(QuietHours{ChannelID: 4, Start: " 23:00", End: "07:00\t",
		Timezone: " Europe/Amsterdam ", During: "  "})
	if err != nil {
		t.Fatalf("NormaliseQuietHours: %v", err)
	}
	want := QuietHours{ChannelID: 4, Start: "23:00", End: "07:00", Timezone: "Europe/Amsterdam", During: QuietHold}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	got, err = NormaliseQuietHours(QuietHours{Start: "09:00", End: "17:00", Timezone: "UTC", During: " drop "})
	if err != nil || got.During != QuietDrop {
		t.Errorf("an explicit drop came back as %+v, %v", got, err)
	}
}

func TestQuietHoursRefusalNamesTheField(t *testing.T) {
	ok := QuietHours{Start: "23:00", End: "07:00", Timezone: "UTC", During: QuietHold}
	for _, tc := range []struct {
		name, field string
		mutate      func(*QuietHours)
	}{
		{"bad start", "start", func(q *QuietHours) { q.Start = "7:00" }},
		{"bad end", "end", func(q *QuietHours) { q.End = "25:00" }},
		{"equal bounds", "end", func(q *QuietHours) { q.End = q.Start }},
		{"no timezone", "timezone", func(q *QuietHours) { q.Timezone = "" }},
		{"local timezone", "timezone", func(q *QuietHours) { q.Timezone = "Local" }},
		{"unknown zone", "timezone", func(q *QuietHours) { q.Timezone = "Mars/Olympus" }},
		{"bad during", "during", func(q *QuietHours) { q.During = "snooze" }},
	} {
		q := ok
		tc.mutate(&q)
		_, err := NormaliseQuietHours(q)
		var invalid *QuietHoursError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: error %v is not a *QuietHoursError", tc.name, err)
			continue
		}
		if invalid.Field != tc.field {
			t.Errorf("%s: field = %q, want %q", tc.name, invalid.Field, tc.field)
		}
		if invalid.Error() != invalid.Msg || invalid.Msg == "" {
			t.Errorf("%s: Error() = %q, Msg = %q", tc.name, invalid.Error(), invalid.Msg)
		}
	}
}
