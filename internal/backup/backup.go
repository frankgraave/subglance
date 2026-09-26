// Package backup takes scheduled snapshots of the database and keeps them in
// S3-compatible object storage.
//
// It exists because with hourly rollups kept forever, subglance.db becomes the
// only copy of years of history. `subglance backup <path>` already takes a
// consistent snapshot, but only when someone runs it and only to a file on the
// same machine — the one whose disk is the thing that fails. This package makes
// that snapshot on a timer and puts it somewhere else.
//
// It deliberately does not move old data out of the database or sync it
// anywhere. Hourly buckets are small, so the long history already fits in the
// database; this is a copy for disaster recovery, not an archive.
package backup

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frankgraave/subglance/internal/store"
)

// Defaults, stated here once so config and documentation read the same number.
const (
	// DefaultInterval is one backup a day. Most instances change slowly
	// enough that a day of lost history is an acceptable worst case, and a
	// full copy per day keeps the bucket small.
	DefaultInterval = 24 * time.Hour

	// DefaultKeep is two weeks of daily backups: long enough to go back to
	// before a mistake that took a few days to notice.
	DefaultKeep = 14

	// MinInterval is the shortest interval accepted. Every backup is a full
	// copy and Keep counts copies, so a short interval does not buy more
	// safety — it shrinks the history the bucket covers, which at five
	// minutes and the default Keep would be seventy minutes.
	MinInterval = time.Hour

	// retryAfterFailure is how soon a failed scheduled backup is tried
	// again. Waiting a full interval would turn one bad night into two days
	// without a backup.
	retryAfterFailure = time.Hour

	// maxRunDuration bounds one scheduled run. The transport bounds each
	// phase that waits for a reply, but not an upload body the peer stops
	// reading while it keeps the connection open. Without a deadline such a
	// run never returns, so it is never recorded, retried or reported. An
	// hour is far beyond a healthy upload of any database this tool keeps,
	// and it matches the retry delay, so a stuck run costs at most one slot.
	maxRunDuration = time.Hour
)

// ErrBusy is returned when a backup is requested while one is running.
var ErrBusy = errors.New("a backup is already running")

// objectName is the fixed shape of every object this package writes: the
// UTC time it was taken, so that sorting the names sorts the backups. Pruning
// only ever touches names of exactly this shape, so anything else an operator
// keeps under the same prefix is left alone.
var objectName = regexp.MustCompile(`^subglance-\d{8}T\d{6}Z\.db\.gz$`)

const objectTimeLayout = "20060102T150405Z"

// Options configures New.
type Options struct {
	DB     *store.DB
	Target Target

	AccessKeyID     string
	SecretAccessKey string

	// Interval between scheduled backups. Zero means DefaultInterval.
	Interval time.Duration
	// Keep is how many backups stay in the bucket. Zero means DefaultKeep.
	Keep int

	// StagingDir holds the snapshot while it is compressed and uploaded.
	// The data directory is the right place: it is on a disk known to have
	// room for one database, and the image's /tmp may be a small tmpfs.
	StagingDir string

	Log *slog.Logger
	Now func() time.Time
}

// Status is what an operator needs to know about the last backups.
type Status struct {
	Target string `json:"target"`

	LastSuccess time.Time `json:"last_success,omitzero"`
	LastObject  string    `json:"last_object,omitempty"`
	LastSize    int64     `json:"last_size_bytes,omitempty"`

	// LastError is the most recent failure, cleared by the next success.
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`

	// Failures counts every failed run since the process started.
	Failures uint64 `json:"failures"`
}

// Result describes one completed backup.
type Result struct {
	Object string
	Size   int64
	Pruned int
}

// Backups runs backups to one target.
type Backups struct {
	db       *store.DB
	client   *client
	target   Target
	interval time.Duration
	keep     int
	staging  string
	log      *slog.Logger
	now      func() time.Time

	// runTimeout is maxRunDuration; a field so tests can shorten it.
	runTimeout time.Duration

	running atomic.Bool

	mu     sync.Mutex
	status Status
}

// New validates the options and returns a runner. It does not contact the
// service; the first backup does, and reports what it finds.
func New(opts Options) (*Backups, error) {
	if opts.DB == nil {
		return nil, errors.New("backup: no database")
	}
	if opts.Target.Bucket == "" {
		return nil, errors.New("backup: no target bucket")
	}
	if opts.AccessKeyID == "" || opts.SecretAccessKey == "" {
		return nil, errors.New("backup: an access key ID and a secret access key are both required")
	}
	if opts.StagingDir == "" {
		return nil, errors.New("backup: no staging directory")
	}
	if opts.Interval == 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Keep == 0 {
		opts.Keep = DefaultKeep
	}
	if opts.Interval < MinInterval {
		return nil, fmt.Errorf("backup: interval %s is shorter than the minimum %s", opts.Interval, MinInterval)
	}
	if opts.Keep < 1 {
		return nil, fmt.Errorf("backup: keep must be at least 1, got %d", opts.Keep)
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	c := newClient(opts.Target, credentials{AccessKeyID: opts.AccessKeyID, SecretAccessKey: opts.SecretAccessKey})
	c.now = opts.Now
	return &Backups{
		db:       opts.DB,
		client:   c,
		target:   opts.Target,
		interval: opts.Interval,
		keep:     opts.Keep,
		staging:  opts.StagingDir,
		log:      opts.Log,
		now:      opts.Now,
		status:   Status{Target: opts.Target.String()},

		runTimeout: maxRunDuration,
	}, nil
}

// Status returns a copy of the current status.
func (b *Backups) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

// RunOnce takes one backup now: snapshot, compress, upload, prune.
//
// It refuses with ErrBusy rather than queueing when one is already running. A
// second copy of the same moment is worth nothing, and two uploads competing
// for the same disk and uplink make both slower.
//
// Pruning happens only after the upload succeeded, so a failed run never
// reduces the number of good backups in the bucket.
func (b *Backups) RunOnce(ctx context.Context) (Result, error) {
	if !b.running.CompareAndSwap(false, true) {
		return Result{}, ErrBusy
	}
	defer b.running.Store(false)

	res, err := b.runOnce(ctx)
	b.record(res, err)
	return res, err
}

func (b *Backups) runOnce(ctx context.Context) (Result, error) {
	taken := b.now().UTC()
	name := "subglance-" + taken.Format(objectTimeLayout) + ".db.gz"
	key := b.target.Prefix + name

	snapshot := filepath.Join(b.staging, fmt.Sprintf(".backup-%d.db", taken.UnixNano()))
	compressed := snapshot + ".gz"
	defer func() {
		_ = os.Remove(snapshot)
		_ = os.Remove(compressed)
	}()

	if _, err := b.db.BackupTo(ctx, snapshot); err != nil {
		return Result{}, fmt.Errorf("snapshot: %w", err)
	}
	size, sum, err := gzipFile(snapshot, compressed)
	if err != nil {
		return Result{}, fmt.Errorf("compress: %w", err)
	}
	// The uncompressed snapshot can go before the upload: only the
	// compressed copy is needed from here, and holding both doubles the
	// space a backup needs on the disk that is most likely to be tight.
	_ = os.Remove(snapshot)

	if err := b.client.put(ctx, key, compressed, sum); err != nil {
		return Result{}, fmt.Errorf("upload %s: %w", key, err)
	}
	res := Result{Object: name, Size: size}

	pruned, err := b.prune(ctx)
	res.Pruned = pruned
	if err != nil {
		// The backup itself is safe in the bucket, which record() notes
		// as a success; the failure is still returned, because a bucket
		// that is never pruned grows until someone notices the bill.
		return res, &pruneError{err: err}
	}
	return res, nil
}

// pruneError marks a run whose upload succeeded but whose pruning did not.
type pruneError struct{ err error }

func (e *pruneError) Error() string { return "remove old backups: " + e.err.Error() }
func (e *pruneError) Unwrap() error { return e.err }

// IsPruneError reports whether err means the backup was uploaded but older
// backups could not be removed.
func IsPruneError(err error) bool {
	var pe *pruneError
	return errors.As(err, &pe)
}

func (b *Backups) record(res Result, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	if err == nil || IsPruneError(err) {
		b.status.LastSuccess = now
		b.status.LastObject = res.Object
		b.status.LastSize = res.Size
		b.status.LastError = ""
		b.status.LastErrorAt = time.Time{}
	}
	if err != nil {
		b.status.LastError = err.Error()
		b.status.LastErrorAt = now
		b.status.Failures++
	}
}

// List returns the names of the backups under the target prefix, oldest
// first. Objects that are not named like a backup are left out.
func (b *Backups) List(ctx context.Context) ([]string, error) {
	return listBackups(ctx, b.client, b.target)
}

func listBackups(ctx context.Context, c *client, t Target) ([]string, error) {
	objs, err := c.list(ctx, t.Prefix+"subglance-")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, o := range objs {
		name := strings.TrimPrefix(o.Key, t.Prefix)
		if objectName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// prune deletes the oldest backups beyond Keep.
func (b *Backups) prune(ctx context.Context) (int, error) {
	names, err := b.List(ctx)
	if err != nil {
		return 0, err
	}
	excess := len(names) - b.keep
	deleted := 0
	for i := 0; i < excess; i++ {
		if err := b.client.delete(ctx, b.target.Prefix+names[i]); err != nil {
			return deleted, fmt.Errorf("delete %s: %w", names[i], err)
		}
		deleted++
	}
	return deleted, nil
}

// Run takes a backup every interval until ctx is cancelled. onResult, when
// not nil, is called once per completed run: with the error when it failed,
// with nil when it uploaded cleanly. The server uses it to send an alert,
// because a backup that fails silently is not a backup, and uses the nil to
// end a failing streak. A run whose upload succeeded but whose pruning failed
// reports the prune error, so it does not end a streak. Each run is bounded by
// maxRunDuration; one that exceeds it is reported and retried as a failure.
//
// The first run is timed from the newest backup already in the bucket, not
// from process start. Otherwise an instance restarted more often than the
// interval — a nightly redeploy with a daily interval — would never reach its
// first backup, and one restarted in a crash loop would upload on every start.
// When the bucket cannot be read, the first run is taken straight away: it
// will most likely fail too, and fail loudly, which is what an operator who
// just set this up needs to see.
func (b *Backups) Run(ctx context.Context, onResult func(error)) {
	b.removeStaleStaging()
	next := b.firstRun(ctx)
	b.log.Info("scheduled backups enabled",
		"target", b.target.String(), "interval", b.interval, "keep", b.keep,
		"next", next.UTC().Format(time.RFC3339))

	for {
		wait := max(next.Sub(b.now()), 0)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		runCtx, cancelRun := context.WithTimeout(ctx, b.runTimeout)
		res, err := b.RunOnce(runCtx)
		cancelRun()
		switch {
		case err == nil:
			b.log.Info("backup uploaded", "object", res.Object, "bytes", res.Size, "pruned", res.Pruned)
			next = b.now().Add(b.interval)
			if onResult != nil {
				onResult(nil)
			}
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrBusy):
			next = b.now().Add(retryAfterFailure)
		case IsPruneError(err):
			b.log.Error("backup uploaded, but old backups could not be removed",
				"object", res.Object, "error", err)
			next = b.now().Add(b.interval)
			if onResult != nil {
				onResult(err)
			}
		default:
			retry := min(b.interval, retryAfterFailure)
			b.log.Error("backup failed", "target", b.target.String(), "error", err, "retry_in", retry)
			next = b.now().Add(retry)
			if onResult != nil {
				onResult(err)
			}
		}
	}
}

// stagingPatterns match the files runOnce writes into the staging directory.
var stagingPatterns = []string{".backup-*.db", ".backup-*.db.gz"}

// removeStaleStaging deletes staging files left by a run that never reached
// its deferred cleanup: a process killed mid-backup by SIGKILL, the OOM killer
// or a container stop timeout. Each later run picks a new name, so nothing
// else would ever remove them, and each is a full copy of the database.
//
// It holds the running flag while it does so, so it cannot delete the files
// of a run that is in progress.
func (b *Backups) removeStaleStaging() {
	if !b.running.CompareAndSwap(false, true) {
		return
	}
	defer b.running.Store(false)
	for _, pattern := range stagingPatterns {
		matches, err := filepath.Glob(filepath.Join(b.staging, pattern))
		if err != nil {
			continue
		}
		for _, m := range matches {
			if err := os.Remove(m); err != nil && !errors.Is(err, os.ErrNotExist) {
				b.log.Warn("could not remove a leftover backup staging file", "path", m, "error", err)
				continue
			}
			b.log.Info("removed a leftover backup staging file", "path", m)
		}
	}
}

func (b *Backups) firstRun(ctx context.Context) time.Time {
	now := b.now()
	names, err := b.List(ctx)
	if err != nil || len(names) == 0 {
		return now
	}
	newest := names[len(names)-1]
	stamp := strings.TrimSuffix(strings.TrimPrefix(newest, "subglance-"), ".db.gz")
	taken, err := time.Parse(objectTimeLayout, stamp)
	if err != nil {
		return now
	}
	// A clock that moved backwards, or a backup stamped in the future by
	// another instance, must not push the first run past one interval.
	if taken.After(now) {
		return now.Add(b.interval)
	}
	return taken.Add(b.interval)
}

// gzipFile compresses src into dst and returns the compressed size and its
// SHA-256, which the upload signature covers.
func gzipFile(src, dst string) (int64, string, error) {
	// Both paths are built in runOnce from the configured data directory
	// and a timestamp; nothing in them comes from a request.
	in, err := os.Open(src) //nolint:gosec // path built from the data dir
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path built from the data dir
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	zw := gzip.NewWriter(io.MultiWriter(out, h))
	if _, err := io.Copy(zw, in); err != nil {
		_ = out.Close()
		return 0, "", err
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return 0, "", err
	}
	info, err := out.Stat()
	if err != nil {
		_ = out.Close()
		return 0, "", err
	}
	if err := out.Close(); err != nil {
		return 0, "", err
	}
	return info.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

// AlertGate decides when a failing backup is worth a notification.
//
// Once per failing streak, and again every day it keeps failing. A failed run
// is retried every hour, and an alert per retry is the fastest way to teach
// someone to mute the channel that is also meant to wake them for an outage.
// But a streak that is still going a day later is news again: that is a day
// without a backup.
type AlertGate struct {
	mu   sync.Mutex
	last time.Time
}

const realertAfter = 24 * time.Hour

// ShouldAlert reports whether a failure at now should send a notice, and
// records it if so.
func (a *AlertGate) ShouldAlert(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.last.IsZero() && now.Sub(a.last) < realertAfter {
		return false
	}
	a.last = now
	return true
}

// Forget undoes the last ShouldAlert, for a notice that could not be sent.
// The next failure then tries again instead of waiting a day.
func (a *AlertGate) Forget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = time.Time{}
}
