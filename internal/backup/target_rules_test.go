package backup

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Each refusal names the part that is wrong, so an operator can fix the
// variable without reading the source.
func TestParseTargetRefusesABucketOrRegionEveryUploadRefuses(t *testing.T) {
	cases := []struct {
		name, raw, endpoint, region, want string
	}{
		{"endpoint written into the target", "s3://minio.local:9000/backups", "", "", "host and port"},
		{"endpoint in the target beside an endpoint", "s3://minio.local:9000/backups", "https://minio.example", "", "host and port"},
		{"an IPv6 address as the bucket", "s3://[::1]/x", "", "", "host and port"},
		{"a character no bucket name takes", "s3://my!bucket/x", "https://minio.example", "", `contains "!"`},
		{"AWS: a bucket that starts with a hyphen", "s3://-bucket/x", "", "", "starts with a hyphen"},
		{"AWS: a bucket that ends with a hyphen", "s3://bucket-/x", "", "", "ends with a hyphen"},
		{"AWS: a region with spaces", "s3://bucket/x", "", "eu central 1", `contains " "`},
		{"AWS: a region with spaces, dotted bucket", "s3://b.example/x", "", "eu central 1", `contains " "`},
		{"AWS: a region with a trailing dot", "s3://bucket/x", "", "eu-west-1.", "empty label"},
		{"a region with a slash", "s3://bucket/x", "", "eu-central-1/", `contains "/"`},
		{"a region with a slash, any service", "s3://bucket/x", "https://minio.example", "eu/central", `contains "/"`},
		{"a region with a comma, any service", "s3://bucket/x", "https://minio.example", "eu,central", `contains ","`},
		{"a region with a newline, any service", "s3://bucket/x", "https://minio.example", "auto\n", "control character"},
		{"a region with a tab, any service", "s3://bucket/x", "https://minio.example", "auto\t", `control character "\t"`},
		{"a region with a DEL, any service", "s3://bucket/x", "https://minio.example", "eu\x7fwest", "control character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseTarget(c.raw, c.endpoint, c.region)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ParseTarget(%q, %q, %q) = %v, want an error containing %q",
					c.raw, c.endpoint, c.region, err, c.want)
			}
		})
	}
}

// Names one service accepts and another refuses are left to the service:
// only what fails for every service, or for the hostname AWS is asked at, is
// refused at startup.
func TestParseTargetKeepsWhatSomeServiceAccepts(t *testing.T) {
	cases := []struct{ raw, endpoint, region string }{
		{"s3://my-bucket/prod", "", "eu-west-1"},
		// Legacy us-east-1 names: capitals and underscores, path style
		// because of the dot.
		{"s3://My_Legacy.Bucket/x", "", ""},
		{"s3://b_c/x", "", ""},
		{"s3://123/x", "", "us-east-1"},
		// Behind an endpoint the bucket is a path segment, never a label.
		{"s3://-bucket/x", "https://minio.example", ""},
		{"s3://b_c/x", "https://minio.example", ""},
		{"s3://b/x", "https://example.r2.cloudflarestorage.com", "auto"},
		{"s3://b/x", "https://s3.eu-central-003.backblazeb2.com", "eu-central-003"},
		// botocore#2909: a provider whose region has an underscore.
		{"s3://b/x", "https://s3.example.ru", "zg_publicru"},
		{"s3://b/x", "https://ceph.example", "default:default-placement"},
	}
	for _, c := range cases {
		if _, err := ParseTarget(c.raw, c.endpoint, c.region); err != nil {
			t.Errorf("ParseTarget(%q, %q, %q) = %v, want it accepted", c.raw, c.endpoint, c.region, err)
		}
	}
}

// The refusals are not a style rule: each target refused for AWS S3 builds a
// request that is never sent, and each one accepted reaches the resolver.
// The resolver here dials nothing, so the test stays off the network; a dial
// attempt is the evidence that the name was usable.
func TestAWSTargetRefusalsAgreeWithTheRequest(t *testing.T) {
	cases := []struct {
		target  Target
		refused bool
	}{
		{Target{Bucket: "-bucket", Region: "us-east-1"}, true},
		{Target{Bucket: "bucket-", Region: "us-east-1"}, true},
		{Target{Bucket: "bucket:9000", Region: "us-east-1"}, true},
		{Target{Bucket: "[::1]", Region: "us-east-1"}, true},
		{Target{Bucket: "bucket", Region: "eu central 1"}, true},
		{Target{Bucket: "b.example", Region: "eu central 1"}, true},
		{Target{Bucket: "bucket", Region: "eu-central-1/"}, true},
		{Target{Bucket: "bucket", Region: "eu-west-1."}, true},
		{Target{Bucket: "my-bucket", Region: "eu-west-1"}, false},
		{Target{Bucket: "My_Legacy.Bucket", Region: "us-east-1"}, false},
		{Target{Bucket: "b_c", Region: "us-east-1"}, false},
	}
	for _, c := range cases {
		t.Run(c.target.Bucket+" in "+c.target.Region, func(t *testing.T) {
			raw := "s3://" + c.target.Bucket + "/x"
			_, parseErr := ParseTarget(raw, "", c.target.Region)
			if refused := parseErr != nil; refused != c.refused {
				t.Fatalf("ParseTarget(%q, region %q) = %v, want refused = %v", raw, c.target.Region, parseErr, c.refused)
			}

			var dialled atomic.Bool
			resolver := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
				dialled.Store(true)
				return nil, errors.New("no network in this test")
			}}
			cl := newClient(c.target, credentials{AccessKeyID: "k", SecretAccessKey: "s"})
			cl.http = &http.Client{Transport: &http.Transport{
				DialContext: (&net.Dialer{Resolver: resolver}).DialContext,
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := cl.list(ctx, "x/")
			if err == nil {
				t.Fatal("list succeeded with no network")
			}
			if sent := dialled.Load(); sent == c.refused {
				t.Errorf("resolver asked = %v (request error %v), want %v", sent, err, !c.refused)
			}
		})
	}
}

// Behind an endpoint the only region refused is one that breaks the request
// for every service. A control character is refused by net/http before the
// request leaves the process, so the service never sees it.
func TestAControlCharacterInTheRegionNeverReachesTheService(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	region := "auto\n"
	if _, err := ParseTarget("s3://b/x", srv.URL, region); err == nil {
		t.Fatalf("ParseTarget accepted region %q", region)
	}
	cl := newClient(Target{Bucket: "b", Endpoint: srv.URL, Region: region}, credentials{AccessKeyID: "k", SecretAccessKey: "s"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cl.list(ctx, "x/"); err == nil {
		t.Error("list succeeded")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the service received %d requests, want 0", n)
	}
}
