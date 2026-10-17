package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
)

// countingTx counts the statements a channel assignment helper sends.
type countingTx struct {
	tx      *sql.Tx
	lookups int
	inserts int
}

func (c *countingTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "INSERT") {
		c.inserts++
	}
	return c.tx.ExecContext(ctx, query, args...)
}

func (c *countingTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.lookups++
	return c.tx.QueryRowContext(ctx, query, args...)
}

// A request that repeats a channel id costs what the same request naming it
// once costs. Each repeat used to cost a lookup and an insert on the single
// writer connection, so a 1 MiB body of one id held every other write on the
// instance for about 8 s.
func TestChannelAssignmentQueriesARepeatedIDOnce(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	b := seedChannel(t, db, "b")
	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	rule := seedRule(t, db, "env", "prod")

	ids := []int64{b.ID}
	for range 1000 {
		ids = append(ids, a.ID, b.ID)
	}

	helpers := map[string]struct {
		replace func(context.Context, txQuerier, []int64) error
		stored  func() []int64
	}{
		"monitor": {
			replace: func(ctx context.Context, tx txQuerier, ids []int64) error {
				return replaceMonitorChannels(ctx, tx, m.ID, ids)
			},
			stored: func() []int64 {
				got, err := db.ListMonitorChannels(t.Context(), m.ID)
				if err != nil {
					t.Fatalf("ListMonitorChannels: %v", err)
				}
				out := []int64{}
				for _, c := range got {
					out = append(out, c.ID)
				}
				return out
			},
		},
		"routing rule": {
			replace: func(ctx context.Context, tx txQuerier, ids []int64) error {
				return replaceRuleChannels(ctx, tx, rule.ID, ids)
			},
			stored: func() []int64 {
				got, err := db.GetRoutingRule(t.Context(), rule.ID)
				if err != nil {
					t.Fatalf("GetRoutingRule: %v", err)
				}
				return got.ChannelIDs
			},
		},
	}
	for name, h := range helpers {
		t.Run(name, func(t *testing.T) {
			tx, err := db.Writer.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			counted := &countingTx{tx: tx}
			if err := h.replace(t.Context(), counted, ids); err != nil {
				t.Fatalf("replace: %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if counted.lookups != 2 || counted.inserts != 2 {
				t.Errorf("%d ids naming 2 channels cost %d lookups and %d inserts, want 2 and 2",
					len(ids), counted.lookups, counted.inserts)
			}
			got := h.stored()
			slices.Sort(got)
			if want := []int64{a.ID, b.ID}; !slices.Equal(got, want) {
				t.Errorf("stored channels = %v, want %v", got, want)
			}
		})
	}
}

// Dropping repeats keeps the order of first appearance, so the refusal for an
// unknown id names the first one the client sent, as it did before.
func TestChannelAssignmentNamesTheFirstUnknownID(t *testing.T) {
	db := openTestDB(t)
	a := seedChannel(t, db, "a")
	m, err := db.CreateMonitor(t.Context(), Monitor{
		Name: "site", Type: "http", Target: "https://example.com", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	ids := []int64{a.ID, 4343, a.ID, 4242, 4343}

	err = db.SetMonitorChannels(t.Context(), m.ID, ids)
	if !errors.Is(err, ErrUnknownChannel) || !strings.HasSuffix(err.Error(), ": 4343") {
		t.Errorf("monitor: err = %v, want ErrUnknownChannel naming 4343", err)
	}
	_, err = db.CreateRoutingRule(t.Context(), RoutingRule{TagKey: "env", TagValue: "dev", ChannelIDs: ids})
	if !errors.Is(err, ErrUnknownChannel) || !strings.HasSuffix(err.Error(), ": 4343") {
		t.Errorf("routing rule: err = %v, want ErrUnknownChannel naming 4343", err)
	}
}

func TestDistinctIDsKeepsFirstAppearance(t *testing.T) {
	cases := []struct{ in, want []int64 }{
		{nil, []int64{}},
		{[]int64{3, 1, 3, 2, 1}, []int64{3, 1, 2}},
		{[]int64{5, 5, 5}, []int64{5}},
	}
	for _, tc := range cases {
		if got := distinctIDs(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("distinctIDs(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
