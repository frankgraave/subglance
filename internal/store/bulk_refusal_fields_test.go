package store

import (
	"errors"
	"testing"
)

// A refused tag or channel operation carries the request field it is about,
// and still matches its sentinel, so store callers that test errors.Is keep
// working while the API places the message.
func TestBulkOperationRefusalsCarryTheirField(t *testing.T) {
	db := openTestDB(t)
	ops := seedChannel(t, db, "ops")
	a := seedLinkedMonitor(t, db, "a")

	for name, tc := range map[string]struct {
		op    TagOperation
		field string
	}{
		"unknown action":     {TagOperation{Action: "explode", MonitorIDs: []int64{a.ID}, Key: "env", Value: "x"}, "action"},
		"global selection":   {TagOperation{Action: "rename_key", MonitorIDs: []int64{a.ID}, Key: "env", NewKey: "x"}, "monitor_ids"},
		"empty selection":    {TagOperation{Action: "apply", Key: "env", Value: "x"}, "monitor_ids"},
		"value on a rename":  {TagOperation{Action: "rename_key", Key: "env", Value: "x", NewKey: "y"}, "value"},
		"new_key on apply":   {TagOperation{Action: "apply", MonitorIDs: []int64{a.ID}, Key: "env", Value: "x", NewKey: "y"}, "new_key"},
		"new_value on apply": {TagOperation{Action: "apply", MonitorIDs: []int64{a.ID}, Key: "env", Value: "x", NewValue: "y"}, "new_value"},
		"bad key":            {TagOperation{Action: "apply", MonitorIDs: []int64{a.ID}, Key: "bad key", Value: "x"}, "key"},
		"empty value":        {TagOperation{Action: "apply", MonitorIDs: []int64{a.ID}, Key: "env"}, "value"},
		"same new key":       {TagOperation{Action: "rename_key", Key: "env", NewKey: "ENV"}, "new_key"},
		"same new value":     {TagOperation{Action: "rename_value", Key: "env", Value: "p", NewValue: "p"}, "new_value"},
	} {
		t.Run("tags/"+name, func(t *testing.T) {
			_, _, err := db.TransformTags(t.Context(), tc.op, "", true)
			var invalid *TagOperationError
			if !errors.As(err, &invalid) || !errors.Is(err, ErrInvalidTagOperation) {
				t.Fatalf("err = %v, want a *TagOperationError matching ErrInvalidTagOperation", err)
			}
			if invalid.Field != tc.field {
				t.Errorf("field = %q, want %q (%s)", invalid.Field, tc.field, invalid.Msg)
			}
		})
	}

	for name, tc := range map[string]struct {
		op    ChannelOperation
		field string
	}{
		"unknown action":  {ChannelOperation{Action: "replace", MonitorIDs: []int64{a.ID}, ChannelID: ops.ID}, "action"},
		"no channel":      {ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID}}, "channel_id"},
		"empty selection": {ChannelOperation{Action: "add", ChannelID: ops.ID}, "monitor_ids"},
		"duplicate id":    {ChannelOperation{Action: "add", MonitorIDs: []int64{a.ID, a.ID}, ChannelID: ops.ID}, "monitor_ids"},
	} {
		t.Run("channels/"+name, func(t *testing.T) {
			_, _, err := db.ChangeMonitorChannels(t.Context(), tc.op, "", true)
			var invalid *ChannelOperationError
			if !errors.As(err, &invalid) || !errors.Is(err, ErrInvalidChannelOperation) {
				t.Fatalf("err = %v, want a *ChannelOperationError matching ErrInvalidChannelOperation", err)
			}
			if invalid.Field != tc.field {
				t.Errorf("field = %q, want %q (%s)", invalid.Field, tc.field, invalid.Msg)
			}
		})
	}
}
