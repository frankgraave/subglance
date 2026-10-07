// Package configfile turns an instance's configuration into a YAML document
// and back: monitors, channels, routing rules and maintenance windows, without
// history, users or credentials.
//
// The file is for moving a setup between instances and for keeping it in a
// repository, where it can be diffed and edited with ordinary tools. It is not
// a backup: the database copy in internal/backup is that, history included.
//
// Three properties hold for every document this package writes or accepts.
//
//   - It carries a version. A reader refuses a version it does not know rather
//     than guessing what the fields mean, because a file kept in git outlives
//     the binary that wrote it.
//   - It never carries a secret. Anything that proves the right to send or
//     read — a webhook URL, a bot token, a password, a request header, a
//     request body — is written as Placeholder. Deny by default: a value is
//     written out only when its field is on an allowlist of things that name
//     a destination rather than authorise one.
//   - Import only adds and updates. A monitor that is missing from the file
//     is left alone, never deleted.
package configfile

import (
	"time"

	"gopkg.in/yaml.v3"
)

// Version is the only document version this build reads and writes.
//
// Every change to the shape of Document that an older reader would misread
// raises it, and comes with a conversion from the previous version so that a
// file already in someone's repository keeps importing.
const Version = 1

// Placeholder stands in for every value that is withheld from a file.
//
// On import it means "keep whatever this instance already has". When the
// instance has nothing to keep, the object is created without it and the
// dry run names the field, so the gap is visible before anything is written.
const Placeholder = "<fill in after import>"

// Document is one configuration file.
//
// Lists rather than maps keyed by Key: a list keeps the order a person wrote,
// and a duplicate key becomes an error that can be reported instead of a map
// entry that silently replaces the one before it.
type Document struct {
	Version      int           `yaml:"version"`
	Monitors     []Monitor     `yaml:"monitors,omitempty"`
	Channels     []Channel     `yaml:"channels,omitempty"`
	RoutingRules []RoutingRule `yaml:"routing_rules,omitempty"`
	Maintenance  []Maintenance `yaml:"maintenance,omitempty"`
}

// Monitor is a monitor definition as it appears in a file.
//
// Optional fields are pointers so that "omitted" and "zero" stay different
// things. An omitted field keeps the instance's current value when the
// monitor already exists, and takes the same default the API gives a new
// monitor when it does not: a hand-written file that lists only a name and a
// target neither resets tuned settings nor ends up with retries of zero.
// Export writes every field that applies to the monitor's type, so an exported
// file never depends on a default that might move.
type Monitor struct {
	Key    string `yaml:"key"`
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Target string `yaml:"target,omitempty"`

	Enabled           *bool `yaml:"enabled,omitempty"`
	IntervalS         *int  `yaml:"interval_s,omitempty"`
	TimeoutS          *int  `yaml:"timeout_s,omitempty"`
	Retries           *int  `yaml:"retries,omitempty"`
	RecoveryThreshold *int  `yaml:"recovery_threshold,omitempty"`
	RepeatAfterS      *int  `yaml:"repeat_after_s,omitempty"`

	// HTTP only.
	Method          *string           `yaml:"method,omitempty"`
	ExpectedStatus  *string           `yaml:"expected_status,omitempty"`
	Keyword         *string           `yaml:"keyword,omitempty"`
	KeywordMode     *string           `yaml:"keyword_mode,omitempty"`
	FollowRedirects *bool             `yaml:"follow_redirects,omitempty"`
	CaptureResponse *bool             `yaml:"capture_response,omitempty"`
	Headers         map[string]string `yaml:"headers,omitempty"`
	Body            *string           `yaml:"body,omitempty"`

	// JSONAssertion is read through Assertion; see there for why it is a
	// node. Export writes it for every HTTP monitor, null when there is none.
	JSONAssertion yaml.Node `yaml:"json_assertion,omitempty"`

	// HTTP and SSL.
	SSLWarnDays   *int    `yaml:"ssl_warn_days,omitempty"`
	MinTLSVersion *string `yaml:"min_tls_version,omitempty"`

	// DNS only: the record a dns monitor compares. Export writes it for
	// every dns monitor; an import that leaves it out keeps what the
	// monitor has.
	DNS *DNSCheck `yaml:"dns,omitempty"`

	// Push only. The push URL itself is a credential and is never written:
	// a push monitor created by an import is issued a new one.
	PushIntervalS *int `yaml:"push_interval_s,omitempty"`
	PushGraceS    *int `yaml:"push_grace_s,omitempty"`

	// Tags replaces the monitor's tags when present; `{}` clears them.
	// Omitted keeps what the instance has. Export always writes it.
	Tags map[string]string `yaml:"tags"`

	// Channels lists the keys of the channels this monitor alerts through,
	// replacing its current assignments when present; `[]` clears them.
	// Omitted keeps what the instance has. Export always writes it.
	Channels []string `yaml:"channels"`
}

// DNSCheck is a dns monitor's settings as they appear in a file: the same
// three fields, under the same names, as the API's `dns` object.
type DNSCheck struct {
	RecordType string   `yaml:"record_type"`
	Expected   []string `yaml:"expected"`
	Resolver   string   `yaml:"resolver,omitempty"`
}

// Channel is a notification channel as it appears in a file.
type Channel struct {
	Key     string `yaml:"key"`
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Enabled *bool  `yaml:"enabled,omitempty"`

	// Default makes this channel the instance default. Only true is
	// meaningful: an import never clears a default, because a file that
	// simply does not mention one should not leave an instance without it.
	Default bool `yaml:"default,omitempty"`

	// Config holds the type-specific settings. Every value outside
	// PublicConfigKeys is written as Placeholder. When present it replaces
	// the channel's settings, with each Placeholder resolved to the value the
	// instance already holds for that field; omitted keeps them all.
	Config map[string]string `yaml:"config,omitempty"`

	QuietHours *QuietHours `yaml:"quiet_hours,omitempty"`
}

// QuietHours is a channel's daily quiet window.
type QuietHours struct {
	Start    string `yaml:"start"`
	End      string `yaml:"end"`
	Timezone string `yaml:"timezone"`
	During   string `yaml:"during"`
}

// RoutingRule routes monitors carrying a tag to channels. A rule is matched by
// its tag pair, which the instance already keeps unique. A list that is present
// replaces the rule's; a list that is omitted is left alone.
type RoutingRule struct {
	TagKey   string `yaml:"tag_key"`
	TagValue string `yaml:"tag_value"`
	// Channels lists channel keys. Omitted keeps a rule's current channels;
	// export always writes it.
	Channels []string `yaml:"channels"`
	// Exclude lists monitor keys the rule leaves out. Omitted keeps the
	// rule's current exclusions; `[]` clears them. Export always writes it,
	// so importing an export also clears exclusions the source rule lacks.
	Exclude []string `yaml:"exclude"`
}

// Maintenance is a maintenance window. It targets one monitor, by key, or one
// tag pair.
//
// Windows have no key. Two windows are the same window when every field
// matches, so importing a file twice adds nothing, and an edited window is
// added next to the old one rather than replacing it: a window cannot be told
// apart from a second window with a similar schedule, and guessing wrong would
// silently drop someone's maintenance.
type Maintenance struct {
	Name     string `yaml:"name"`
	Monitor  string `yaml:"monitor,omitempty"`
	TagKey   string `yaml:"tag_key,omitempty"`
	TagValue string `yaml:"tag_value,omitempty"`

	// One-off windows.
	StartsAt *time.Time `yaml:"starts_at,omitempty"`
	EndsAt   *time.Time `yaml:"ends_at,omitempty"`

	// Weekly windows. Weekdays count from Sunday = 0.
	Timezone        string `yaml:"timezone,omitempty"`
	Weekdays        []int  `yaml:"weekdays,omitempty"`
	LocalTime       string `yaml:"local_time,omitempty"`
	DurationMinutes int    `yaml:"duration_minutes,omitempty"`
}
