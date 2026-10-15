package backup

import (
	"strings"
	"testing"
)

// The parts of a key pair written into a backup URL. A refusal that quotes
// any of them has written a credential to the start-up log.
const (
	urlKeyID  = "AKIAEXAMPLEKEY"
	urlSecret = "s3cr3tpa55"
)

// A user name and password in the endpoint are never used: every request is
// signed, and the Authorization header that sets wins over a URL's user
// part. They are refused, and no refusal of a backup URL quotes them.
func TestParseTargetRefusesCredentialsWithoutQuotingThem(t *testing.T) {
	const pair = urlKeyID + ":" + urlSecret
	cases := []struct {
		name, raw, endpoint, want string
		// keyIDShown is set where the address has no "@", so the key ID
		// cannot be told apart from a hostname and is shown as one.
		keyIDShown bool
	}{
		{"key pair in the endpoint", "s3://bucket/x", "https://" + pair + "@minio.local:9000",
			`backup endpoint "https://minio.local:9000" holds a user name or password`, false},
		{"key ID alone in the endpoint", "s3://bucket/x", "https://" + urlKeyID + "@minio.local:9000",
			`backup endpoint "https://minio.local:9000" holds a user name or password`, false},
		{"key pair in an endpoint with a path", "s3://bucket/x", "https://" + pair + "@minio.local:9000/backups",
			`backup endpoint "https://minio.local:9000/…" holds a user name or password`, false},
		{"key pair in an endpoint with port 0", "s3://bucket/x", "https://" + pair + "@minio.local:0",
			"holds a user name or password", false},
		{"key pair in an endpoint of another scheme", "s3://bucket/x", "ftp://" + pair + "@minio.local",
			"holds a user name or password", false},
		{"a slash in the password", "s3://bucket/x", "https://" + urlKeyID + ":s3cr3t/pa55@minio.local:9000",
			`backup endpoint "https://***@minio.local:***": want an http or https URL`, false},
		{"the @ left out", "s3://bucket/x", "https://" + pair,
			`backup endpoint "https://` + urlKeyID + `:***": want an http or https URL`, true},
		{"key pair in the target", "s3://" + pair + "@bucket/x", "",
			`backup target "s3://bucket/…" holds a user name or password`, false},
		{"key ID alone in the target", "s3://" + urlKeyID + "@bucket", "",
			`backup target "s3://bucket" holds a user name or password`, false},
		{"a slash in the password in the target", "s3://" + urlKeyID + ":s3cr3t/pa55@bucket/x", "",
			`backup target "s3://***@bucket/…": want s3://bucket`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseTarget(c.raw, c.endpoint, "")
			if err == nil {
				t.Fatalf("ParseTarget(%q, %q) accepted the credentials", c.raw, c.endpoint)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.want) {
				t.Errorf("ParseTarget(%q, %q) = %q, want it to contain %q", c.raw, c.endpoint, msg, c.want)
			}
			for _, secret := range []string{urlSecret, "s3cr3t", "pa55"} {
				if strings.Contains(msg, secret) {
					t.Errorf("the refusal quotes the password %q: %s", secret, msg)
				}
			}
			if !c.keyIDShown && strings.Contains(msg, urlKeyID) {
				t.Errorf("the refusal quotes the key ID: %s", msg)
			}
			if strings.Contains(msg, `invalid port "`) {
				t.Errorf("the refusal quotes the text the parser took for a port: %s", msg)
			}
		})
	}
}

// The refusal says where the key pair goes, so the operator can move it
// rather than delete it.
func TestACredentialRefusalNamesWhereTheKeyPairGoes(t *testing.T) {
	for _, c := range []struct{ raw, endpoint string }{
		{"s3://bucket/x", "https://" + urlKeyID + ":" + urlSecret + "@minio.local"},
		{"s3://" + urlKeyID + ":" + urlSecret + "@bucket/x", ""},
	} {
		_, err := ParseTarget(c.raw, c.endpoint, "")
		if err == nil || !strings.Contains(err.Error(), "SUBGLANCE_BACKUP_ACCESS_KEY_ID") {
			t.Errorf("ParseTarget(%q, %q) = %v, want it to name SUBGLANCE_BACKUP_ACCESS_KEY_ID", c.raw, c.endpoint, err)
		}
	}
}

// An address that parsed with no user part carries no credential in its
// authority, so its refusals still quote it whole: the path or the port
// being refused is what the operator has to find.
func TestEndpointRefusalsWithoutCredentialsQuoteTheEndpoint(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://minio.local:9000/backups": `backup endpoint "https://minio.local:9000/backups": want the service URL without a path`,
		"https://minio.local:0":            `backup endpoint "https://minio.local:0": port 0 is out of range`,
		"ftp://minio.local":                `backup endpoint "ftp://minio.local": want an http or https URL`,
	} {
		_, err := ParseTarget("s3://bucket/x", endpoint, "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseTarget(endpoint %q) = %v, want it to contain %q", endpoint, err, want)
		}
	}
}
