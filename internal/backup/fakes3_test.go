package backup

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is an in-process, path-style S3 endpoint holding one bucket.
//
// It checks that every request carries a SigV4 Authorization header for the
// right key and region, and that an upload's body matches the payload hash the
// signature covers — the property that makes a corrupted upload fail instead
// of becoming a backup. It does not recompute signatures: doing that with the
// signer under test would agree with any mistake in it. sigv4_test.go checks
// the signature itself against AWS's published examples.
type fakeS3 struct {
	t      *testing.T
	bucket string

	mu      sync.Mutex
	objects map[string][]byte
	// failPut and failDelete make the next matching calls fail with 500.
	failPut    int
	failDelete int
	// pageSize caps a listing page, to exercise continuation tokens.
	pageSize int
	puts     int
}

func newFakeS3(t *testing.T, bucket string) (*fakeS3, *httptest.Server) {
	f := &fakeS3{t: t, bucket: bucket, objects: map[string][]byte{}, pageSize: 1000}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ks []string
	for k := range f.objects {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=test-key/") ||
		!strings.Contains(auth, "/test-region/s3/aws4_request,") {
		f.fail(w, http.StatusForbidden, "AccessDenied", "bad authorization: "+auth)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != f.bucket {
		f.fail(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPut && key != "":
		body, _ := io.ReadAll(r.Body)
		if hexSHA256(body) != r.Header.Get("X-Amz-Content-Sha256") {
			f.fail(w, http.StatusBadRequest, "XAmzContentSHA256Mismatch", "payload hash does not match")
			return
		}
		if f.failPut > 0 {
			f.failPut--
			f.fail(w, http.StatusInternalServerError, "InternalError", "try again")
			return
		}
		f.puts++
		f.objects[key] = body
	case r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2":
		f.list(w, r)
	case r.Method == http.MethodGet:
		body, ok := f.objects[key]
		if !ok {
			f.fail(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		_, _ = w.Write(body)
	case r.Method == http.MethodDelete:
		if f.failDelete > 0 {
			f.failDelete--
			f.fail(w, http.StatusInternalServerError, "InternalError", "try again")
			return
		}
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		f.fail(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (f *fakeS3) list(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	after := r.URL.Query().Get("continuation-token")
	var ks []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) && k > after {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	type content struct {
		Key  string
		Size int
	}
	out := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		Contents              []content
		IsTruncated           bool
		NextContinuationToken string `xml:",omitempty"`
	}{}
	if len(ks) > f.pageSize {
		ks = ks[:f.pageSize]
		out.IsTruncated = true
		out.NextContinuationToken = ks[len(ks)-1]
	}
	for _, k := range ks {
		out.Contents = append(out.Contents, content{Key: k, Size: len(f.objects[k])})
	}
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}

func (f *fakeS3) fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<Error><Code>"+code+"</Code><Message>"+msg+"</Message></Error>")
}
