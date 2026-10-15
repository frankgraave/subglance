package backup

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/frankgraave/subglance/internal/checker"
)

// Target is where backups go: a bucket, a key prefix inside it, and the
// service that holds the bucket.
type Target struct {
	// Bucket is the bucket name.
	Bucket string
	// Prefix is prepended to every object key. Empty, or ending in "/".
	Prefix string
	// Endpoint is the service base URL, such as https://s3.eu-central-003.backblazeb2.com.
	// Empty means AWS S3 in Region.
	Endpoint string
	// Region is the signing region. Most S3-compatible services accept
	// anything; AWS needs the bucket's real region, R2 wants "auto".
	Region string
}

// ParseTarget reads an s3://bucket/prefix URL.
//
// The URL form is what every S3 tool prints and accepts, so it is what an
// operator will paste. The prefix gets a trailing slash so that "backups" and
// "backups/" name the same folder instead of one of them producing keys like
// "backupssubglance-…".
func ParseTarget(raw, endpoint, region string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("backup target %q: %w", raw, err)
	}
	if u.Scheme != "s3" || u.Host == "" {
		return Target{}, fmt.Errorf("backup target %q: want s3://bucket or s3://bucket/prefix", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return Target{}, fmt.Errorf("backup target %q: want s3://bucket/prefix with nothing after the prefix", raw)
	}
	if err := checkBucket(u.Host); err != nil {
		return Target{}, fmt.Errorf("backup target %q: %w", raw, err)
	}
	prefix := strings.Trim(u.Path, "/")
	if prefix != "" {
		prefix += "/"
	}
	if endpoint != "" {
		e, err := url.Parse(endpoint)
		if err != nil || (e.Scheme != "https" && e.Scheme != "http") || e.Host == "" {
			return Target{}, fmt.Errorf("backup endpoint %q: want an http or https URL", endpoint)
		}
		if e.Path != "" && e.Path != "/" {
			return Target{}, fmt.Errorf("backup endpoint %q: want the service URL without a path; the bucket goes in the s3:// target", endpoint)
		}
		// A port out of range or a hostname the resolver refuses unasked
		// would fail every upload, and the first sign would be the alert
		// about a backup that never ran.
		if err := checker.ResolvableURL(e); err != nil {
			return Target{}, fmt.Errorf("backup endpoint %q: %w", endpoint, err)
		}
	}
	if region == "" {
		region = "us-east-1"
	}
	if err := checkRegion(region); err != nil {
		return Target{}, fmt.Errorf("backup region %q: %w", region, err)
	}
	t := Target{Bucket: u.Host, Prefix: prefix, Endpoint: strings.TrimRight(endpoint, "/"), Region: region}
	// With no endpoint the bucket and the region are part of the hostname
	// every request goes to, so they are held to the resolver's rule: a
	// bucket that starts with a hyphen or a region with a space in it fails
	// every upload as "no such host" or before the request is built.
	if t.Endpoint == "" {
		host, _ := t.awsHost()
		if err := checker.ResolvableHost(host); err != nil {
			return Target{}, fmt.Errorf("backup target %q in region %q: every request to AWS S3 would fail, "+
				"because %w (for another S3-compatible service, set the backup endpoint)", raw, region, err)
		}
	}
	return t, nil
}

// checkBucket refuses a bucket name no S3 service accepts. The characters
// allowed are the union of every naming rule in use: AWS's current and legacy
// rules (the legacy us-east-1 names allow capitals and underscores), MinIO,
// Cloudflare R2, Backblaze B2 and Ceph's relaxed names. Length and hyphen
// placement differ between them and are left to the service, except where the
// bucket becomes part of a hostname (see ParseTarget).
func checkBucket(bucket string) error {
	if strings.ContainsAny(bucket, ":[]") {
		return fmt.Errorf("bucket %q is a host and port, not a bucket name; "+
			"put the service URL in the backup endpoint and only the bucket in the target, as s3://bucket/prefix", bucket)
	}
	for i := 0; i < len(bucket); i++ {
		c := bucket[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '.', c == '-', c == '_':
		default:
			return fmt.Errorf("bucket %q contains %q; a bucket name takes letters, digits, dots, hyphens and underscores",
				bucket, string(c))
		}
	}
	return nil
}

// checkRegion refuses a signing region that breaks the request for every
// service, whatever region that service expects. The region is one field of
// the signature's credential scope, key/date/region/s3/aws4_request, which a
// verifier splits on "/": a slash adds a field, so the date or the region it
// reads is wrong and the signature is refused. The scope ends at a "," in the
// Authorization header, so a comma cuts the region short in the same way. A
// control character is refused too, a tab included: net/http refuses the
// others in a header value and stops the request before it is sent, and a
// tab is whitespace no region name holds, which a server or proxy reading
// the header may fold or trim. Nothing stricter is applied: an
// S3-compatible service may name its regions as it likes, with underscores
// for instance, and a region AWS is asked in is held to the hostname rule in
// ParseTarget.
func checkRegion(region string) error {
	switch {
	case strings.Contains(region, "/"):
		return errors.New(`contains "/", which splits the signature's credential scope; ` +
			"a region is one name, such as eu-west-1 or auto")
	case strings.Contains(region, ","):
		return errors.New(`contains ",", which ends the signature's credential scope; ` +
			"a region is one name, such as eu-west-1 or auto")
	}
	for i := 0; i < len(region); i++ {
		if c := region[i]; c < ' ' || c == 0x7f {
			return fmt.Errorf("contains the control character %q; a region is one name, such as eu-west-1 or auto", string(c))
		}
	}
	return nil
}

// String is the target as the operator configured it, for log lines. It
// carries no credentials, so it is safe to print.
func (t Target) String() string {
	return "s3://" + t.Bucket + "/" + t.Prefix
}

// client speaks the four S3 calls a backup needs: put, get, list and delete.
type client struct {
	target Target
	creds  credentials
	http   *http.Client
	now    func() time.Time
}

func newClient(t Target, creds credentials) *client {
	return &client{
		target: t,
		creds:  creds,
		// No overall Timeout: an upload of a large database is allowed to
		// take as long as its context allows. What is bounded is each
		// phase that can hang on a dead peer — connecting, the TLS
		// handshake, and waiting for the response headers after the body
		// has been sent.
		http: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute,
			IdleConnTimeout:       90 * time.Second,
		}},
		now: time.Now,
	}
}

// objectURL builds the URL for a key, or for the bucket itself when key is
// empty.
//
// AWS gets virtual-hosted style (bucket.s3.region.amazonaws.com), which is
// the style AWS documents as current. A custom endpoint gets path style
// (endpoint/bucket/key), because that is the style every S3-compatible
// service accepts — MinIO in particular answers nothing else by default. A
// bucket with a dot in its name also gets path style on AWS: in the
// virtual-hosted form the dot becomes a subdomain and the certificate for
// *.s3.amazonaws.com no longer matches.
func (c *client) objectURL(key string) *url.URL {
	escapedKey := uriEncode(key, false)
	if c.target.Endpoint != "" {
		u, _ := url.Parse(c.target.Endpoint) // validated in ParseTarget
		u.Path = "/" + c.target.Bucket + "/" + key
		u.RawPath = "/" + uriEncode(c.target.Bucket, true) + "/" + escapedKey
		return u
	}
	host, pathStyle := c.target.awsHost()
	if pathStyle {
		return &url.URL{Scheme: "https", Host: host,
			Path:    "/" + c.target.Bucket + "/" + key,
			RawPath: "/" + uriEncode(c.target.Bucket, true) + "/" + escapedKey}
	}
	return &url.URL{Scheme: "https", Host: host, Path: "/" + key, RawPath: "/" + escapedKey}
}

// awsHost is the host a request to AWS S3 goes to, and whether the bucket
// goes in the path (true) rather than in the host. ParseTarget holds this
// same host to the resolver's rule, so the two cannot disagree.
func (t Target) awsHost() (host string, pathStyle bool) {
	host = "s3." + t.Region + ".amazonaws.com"
	if strings.Contains(t.Bucket, ".") {
		return host, true
	}
	return t.Bucket + "." + host, false
}

func (c *client) do(req *http.Request, payloadHash string) (*http.Response, error) {
	sign(req, c.creds, c.target.Region, payloadHash, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		return nil, s3Error(req, resp)
	}
	return resp, nil
}

// put uploads the file at path to key. sha256hex is the file's SHA-256, which
// the signature covers, so a body corrupted in transit is refused by the
// server rather than stored as a backup.
func (c *client) put(ctx context.Context, key, path, sha256hex string) error {
	f, err := os.Open(path) //nolint:gosec // the staged snapshot, built from the data dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.objectURL(key).String(), f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := c.do(req, sha256hex)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// get streams the object at key. The caller closes the body.
func (c *client) get(ctx context.Context, key string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.objectURL(key).String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req, emptySHA256)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *client) delete(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.objectURL(key).String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, emptySHA256)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// object is one entry of a listing.
type object struct {
	Key  string `xml:"Key"`
	Size int64  `xml:"Size"`
}

type listResult struct {
	Contents              []object `xml:"Contents"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
}

// list returns every key under prefix, following continuation tokens.
func (c *client) list(ctx context.Context, prefix string) ([]object, error) {
	var out []object
	token := ""
	for {
		u := c.objectURL("")
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.do(req, emptySHA256)
		if err != nil {
			return nil, err
		}
		var page listResult
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read bucket listing: %w", err)
		}
		out = append(out, page.Contents...)
		if !page.IsTruncated {
			return out, nil
		}
		if page.NextContinuationToken == "" || page.NextContinuationToken == token {
			return nil, errors.New("read bucket listing: the service said there is more but gave no way to ask for it")
		}
		token = page.NextContinuationToken
	}
}

// s3Error turns an error response into a message that names the call, the
// status and what the service said, which is usually enough to fix it: an
// AccessDenied, a NoSuchBucket, or a SignatureDoesNotMatch that means the
// region is wrong.
func s3Error(req *http.Request, resp *http.Response) error {
	var body struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = xml.Unmarshal(raw, &body)
	what := req.Method + " " + req.URL.Path
	if body.Code == "" {
		return fmt.Errorf("%s: %s", what, resp.Status)
	}
	msg := fmt.Sprintf("%s: %s: %s", what, resp.Status, body.Code)
	if body.Message != "" {
		msg += ": " + body.Message
	}
	return &statusError{status: resp.StatusCode, msg: msg}
}

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

// isNotFound reports whether err is the service saying the key is not there.
func isNotFound(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == http.StatusNotFound
}
