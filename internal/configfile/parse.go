package configfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxKeyLen bounds a key. Long enough for a descriptive slug, short enough to
// stay readable in a diff and in the dry-run report that lists it.
const MaxKeyLen = 64

// keyPattern is the tag-key alphabet: lowercase alphanumerics with dash,
// underscore and dot inside. A key can then be grepped, used in a shell
// variable name after one substitution, and never needs quoting in YAML.
var keyPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9_.-]*[a-z0-9])?$`)

// ValidKey reports whether s is usable as a monitor or channel key.
func ValidKey(s string) bool {
	return len(s) <= MaxKeyLen && keyPattern.MatchString(s)
}

// DeriveKey turns a name into a key, for objects that do not have one yet.
//
// The result is a lowercase slug ("API (prod)" becomes "api-prod"). It is not
// guaranteed to be unique; taken reports keys already in use, and a numeric
// suffix is added until the key is free. fallback is used when the name has no
// usable characters at all, such as a name written entirely in emoji.
func DeriveKey(name, fallback string, taken func(string) bool) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	base := strings.TrimRight(b.String(), "-")
	// Room for a suffix such as "-9999" without crossing MaxKeyLen.
	const room = MaxKeyLen - 6
	if len(base) > room {
		base = strings.TrimRight(base[:room], "-")
	}
	if base == "" {
		base = fallback
	}
	key := base
	for n := 2; taken(key); n++ {
		key = base + "-" + strconv.Itoa(n)
	}
	return key
}

// Problem is a rejected document. Path points at the offending field in the
// style a person would write it ("monitors[2].key"), so the message can be
// placed without parsing its wording.
type Problem struct {
	Path string
	Msg  string
}

func (p *Problem) Error() string {
	if p.Path == "" {
		return p.Msg
	}
	return p.Path + ": " + p.Msg
}

func problemf(path, format string, args ...any) *Problem {
	return &Problem{Path: path, Msg: fmt.Sprintf(format, args...)}
}

// Parse reads a document and checks everything that can be checked without an
// instance: the version, unknown fields, the key format, and duplicates.
// Whether the values make a valid monitor is the importer's job, because that
// needs the same validation the API applies.
//
// Unknown fields are an error rather than ignored. A file that says
// `interval: 30` instead of `interval_s: 30` would otherwise import cleanly
// and leave the monitor on its old schedule, and the mistake would surface
// weeks later as a slow alert.
func Parse(data []byte) (Document, error) {
	// The version is read first, on its own and leniently. A future version
	// is likely to carry fields this build does not know, and "unknown field"
	// would be the wrong complaint about a file that is simply too new.
	var head struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return Document{}, &Problem{Msg: "not a valid YAML document: " + err.Error()}
	}
	switch {
	case head.Version == 0:
		return Document{}, problemf("version", "is required; this build reads version %d", Version)
	case head.Version != Version:
		return Document{}, problemf("version", "%d is not supported; this build reads version %d. "+
			"A newer file needs a newer SubGlance", head.Version, Version)
	}

	var doc Document
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return Document{}, &Problem{Msg: err.Error()}
	}
	// A second document in the same stream would be silently ignored, and
	// whoever concatenated two files would believe both were imported.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Document{}, &Problem{Msg: "the file holds more than one YAML document; import them one at a time"}
	}

	if err := doc.check(); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (d Document) check() error {
	monitors := map[string]bool{}
	for i, m := range d.Monitors {
		path := fmt.Sprintf("monitors[%d]", i)
		if err := checkKey(path, m.Key, monitors); err != nil {
			return err
		}
	}
	channels := map[string]bool{}
	defaults := 0
	for i, c := range d.Channels {
		path := fmt.Sprintf("channels[%d]", i)
		if err := checkKey(path, c.Key, channels); err != nil {
			return err
		}
		if c.Default {
			defaults++
			if defaults > 1 {
				return problemf(path+".default", "only one channel can be the default")
			}
		}
	}
	// A slug's format is the instance's rule and is checked on import; here
	// only its presence and uniqueness, which need no instance. Slugs compare
	// without case, as the instance stores them.
	slugs := map[string]bool{}
	for i, p := range d.StatusPages {
		path := fmt.Sprintf("status_pages[%d].slug", i)
		slug := strings.ToLower(strings.TrimSpace(p.Slug))
		switch {
		case slug == "":
			return problemf(path, "is required")
		case slugs[slug]:
			return problemf(path, "%q is used twice", slug)
		}
		slugs[slug] = true
	}
	return nil
}

func checkKey(path, key string, seen map[string]bool) error {
	switch {
	case key == "":
		return problemf(path+".key", "is required")
	case !ValidKey(key):
		return problemf(path+".key", "%q must be lowercase letters, digits, dot, dash or underscore, "+
			"starting and ending with a letter or digit, at most %d characters", key, MaxKeyLen)
	case seen[key]:
		return problemf(path+".key", "%q is used twice", key)
	}
	seen[key] = true
	return nil
}

// header opens every exported file. It is a comment, so it costs nothing to a
// reader of the document and tells a person what the placeholders mean.
const header = "# SubGlance configuration. Values shown as \"" + Placeholder + "\" were withheld\n" +
	"# because they are credentials; an import keeps the value the instance already has.\n"

// Marshal renders a document as YAML with the explanatory header.
func Marshal(d Document) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("encode configuration: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode configuration: %w", err)
	}
	return buf.Bytes(), nil
}
