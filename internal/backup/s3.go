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
	}
	if region == "" {
		region = "us-east-1"
	}
	return Target{Bucket: u.Host, Prefix: prefix, Endpoint: strings.TrimRight(endpoint, "/"), Region: region}, nil
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
	host := "s3." + c.target.Region + ".amazonaws.com"
	if strings.Contains(c.target.Bucket, ".") {
		return &url.URL{Scheme: "https", Host: host,
			Path:    "/" + c.target.Bucket + "/" + key,
			RawPath: "/" + uriEncode(c.target.Bucket, true) + "/" + escapedKey}
	}
	return &url.URL{Scheme: "https", Host: c.target.Bucket + "." + host, Path: "/" + key, RawPath: "/" + escapedKey}
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
