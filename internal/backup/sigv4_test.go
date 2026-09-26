package backup

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The three worked examples from the S3 documentation for header-based SigV4
// authentication ("Examples: Signature calculations", AWS S3 API reference).
// Same credentials, bucket and moment for all three; the expected signatures
// are copied from that page. Matching them byte for byte is the only way to
// know the canonical form is right: a fake server that checks signatures with
// this same code would agree with any mistake made here.
//
// The documentation's example keys are split in two so secret scanners do
// not stop on them. They are AWS's published placeholders, not credentials.
var awsExampleCreds = credentials{
	AccessKeyID:     "AKIA" + "IOSFODNN7EXAMPLE",
	SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/" + "bPxRfiCYEXAMPLEKEY",
}

var awsExampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func TestSignMatchesAWSGetObjectExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")

	sign(req, awsExampleCreds, "us-east-1", emptySHA256, awsExampleTime)

	assertAuthorization(t, req,
		"host;range;x-amz-content-sha256;x-amz-date",
		"f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
}

func TestSignMatchesAWSPutObjectExample(t *testing.T) {
	body := "Welcome to Amazon S3."
	req, _ := http.NewRequest(http.MethodPut, "https://examplebucket.s3.amazonaws.com/test$file.text",
		strings.NewReader(body))
	req.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	req.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")

	sign(req, awsExampleCreds, "us-east-1", hexSHA256([]byte(body)), awsExampleTime)

	assertAuthorization(t, req,
		"date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
		"98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd")
}

func TestSignMatchesAWSListObjectsExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)

	sign(req, awsExampleCreds, "us-east-1", emptySHA256, awsExampleTime)

	assertAuthorization(t, req,
		"host;x-amz-content-sha256;x-amz-date",
		"34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7")
}

func assertAuthorization(t *testing.T, req *http.Request, signedHeaders, signature string) {
	t.Helper()
	want := "AWS4-HMAC-SHA256 Credential=" + awsExampleCreds.AccessKeyID + "/20130524/us-east-1/s3/aws4_request," +
		"SignedHeaders=" + signedHeaders + ",Signature=" + signature
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization\n got: %s\nwant: %s", got, want)
	}
}

func TestURIEncode(t *testing.T) {
	cases := []struct {
		in          string
		encodeSlash bool
		want        string
	}{
		{"/a b/c$d", false, "/a%20b/c%24d"},
		{"a/b", true, "a%2Fb"},
		{"~-_.AZaz09", true, "~-_.AZaz09"},
		{"é", true, "%C3%A9"},
	}
	for _, c := range cases {
		if got := uriEncode(c.in, c.encodeSlash); got != c.want {
			t.Errorf("uriEncode(%q, %v) = %q, want %q", c.in, c.encodeSlash, got, c.want)
		}
	}
}
