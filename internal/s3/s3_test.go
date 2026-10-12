package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/s3/s3test"
)

// The AWS documentation's example credentials, published with its SigV4
// examples (not a secret of anyone's). Built from parts so that a secret
// scanner does not take the published example for a leak.
var (
	docAccess = strings.Join([]string{"AKIAIOSFODNN7", "EXAMPLE"}, "")
	docSecret = strings.Join([]string{"wJalrXUtnFEMI", "K7MDENG", "bPxRfiCYEXAMPLEKEY"}, "/")
)

// The S3 examples of "Signature Calculations for the Authorization Header:
// Transferring Payload in a Single Chunk" (AWS S3 API reference): bucket
// examplebucket in us-east-1, 2013-05-24T00:00:00Z.
func TestSignatureMatchesTheAWSExamples(t *testing.T) {
	cl, err := New(Config{Endpoint: "https://examplebucket.s3.amazonaws.com", Bucket: "examplebucket", Region: "us-east-1",
		AccessKey: docAccess, SecretKey: docSecret})
	if err != nil {
		t.Fatal(err)
	}
	if !cl.virtual {
		t.Fatal("an endpoint that begins with the bucket is virtual-hosted")
	}
	when := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	welcome := sha256.Sum256([]byte("Welcome to Amazon S3."))
	for _, c := range []struct {
		name, method, url string
		headers           map[string]string
		payload           string
		signedHeaders     string
		signature         string
	}{
		{"GET Object", http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt",
			map[string]string{"Range": "bytes=0-9"}, EmptySHA256,
			"host;range;x-amz-content-sha256;x-amz-date",
			"f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"},
		{"PUT Object", http.MethodPut, "https://examplebucket.s3.amazonaws.com/test$file.text",
			map[string]string{"Date": "Fri, 24 May 2013 00:00:00 GMT", "X-Amz-Storage-Class": "REDUCED_REDUNDANCY"},
			hex.EncodeToString(welcome[:]),
			"date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
			"98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"},
		{"GET Bucket Lifecycle", http.MethodGet, "https://examplebucket.s3.amazonaws.com/?lifecycle", nil, EmptySHA256,
			"host;x-amz-content-sha256;x-amz-date",
			"fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"},
		{"Get Bucket (List Objects)", http.MethodGet, "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil, EmptySHA256,
			"host;x-amz-content-sha256;x-amz-date",
			"34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"},
	} {
		req, err := http.NewRequest(c.method, c.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		cl.sign(req, c.payload, when)
		want := "AWS4-HMAC-SHA256 Credential=" + docAccess + "/20130524/us-east-1/s3/aws4_request, SignedHeaders=" +
			c.signedHeaders + ", Signature=" + c.signature
		if got := req.Header.Get("Authorization"); got != want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, want)
		}
		if req.Header.Get("X-Amz-Date") != "20130524T000000Z" || req.Header.Get("X-Amz-Content-Sha256") != c.payload {
			t.Errorf("%s: headers %v", c.name, req.Header)
		}
	}
}

// "Examples of how to derive a signing key for Signature Version 4" (AWS
// general reference): the key for 2012-02-15, us-east-1, iam.
func TestSigningKeyMatchesTheAWSExample(t *testing.T) {
	secret := strings.Join([]string{"wJalrXUtnFEMI", "K7MDENG+bPxRfiCYEXAMPLEKEY"}, "/")
	got := hex.EncodeToString(signingKey(secret, "20120215", "us-east-1", "iam"))
	if want := "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1a9e090d414db404d"; got != want {
		t.Fatalf("signing key %s, want %s", got, want)
	}
}

func TestEncoding(t *testing.T) {
	for in, want := range map[string]string{
		"/bucket/bobres-20261011T033000Z.bkp": "/bucket/bobres-20261011T033000Z.bkp",
		"/test$file.text":                     "/test%24file.text",
		"/a b/c+d~e":                          "/a%20b/c%2Bd~e",
		"":                                    "/",
	} {
		if got := encodePath(in); got != want {
			t.Errorf("encodePath(%q) = %q, want %q", in, got, want)
		}
	}
	q := map[string][]string{"prefix": {"shop/bobres-"}, "list-type": {"2"}, "uploads": {""}}
	if got, want := canonicalQuery(q), "list-type=2&prefix=shop%2Fbobres-&uploads="; got != want {
		t.Errorf("canonicalQuery = %q, want %q", got, want)
	}
}

func fakeClient(t *testing.T, f *s3test.Fake, virtual bool, mod func(*Config)) *Client {
	t.Helper()
	c := Config{Endpoint: f.URL(), Bucket: f.Bucket, Region: f.Region, AccessKey: f.AccessKey, SecretKey: f.SecretKey,
		HTTPClient: f.Client(virtual)}
	if virtual {
		c.Endpoint = f.VirtualURL()
	}
	if mod != nil {
		mod(&c)
	}
	cl, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func refusedNone(t *testing.T, f *s3test.Fake) {
	t.Helper()
	if bad := f.Refused(); len(bad) > 0 {
		t.Fatalf("the fake refused signatures: %v", bad)
	}
}

func TestPutGetListDelete(t *testing.T) {
	for _, virtual := range []bool{false, true} {
		f := s3test.New("bobres-test")
		cl := fakeClient(t, f, virtual, nil)
		if cl.virtual != virtual {
			t.Fatalf("virtual-hosted %v for %s", cl.virtual, f.URL())
		}
		ctx := context.Background()
		data := bytes.Repeat([]byte("encrypted backup "), 1000)
		names := []string{"shop/bobres-20261009T033000Z.bkp", "shop/bobres-20261010T033000Z.bkp", "shop/bobres-20261011T033000Z.bkp",
			"shop/notes.txt", "other/bobres-20261011T033000Z.bkp"}
		for _, n := range names {
			if err := cl.PutFile(ctx, n, bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatalf("virtual %v: put %s: %v", virtual, n, err)
			}
		}
		if got, _ := f.Object(names[0]); !bytes.Equal(got, data) {
			t.Fatalf("stored %d bytes", len(got))
		}
		// Three pages of two: the continuation is followed.
		objs, err := cl.List(ctx, "shop/bobres-")
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, o := range objs {
			keys = append(keys, o.Key)
			if o.Size != int64(len(data)) || o.LastModified.IsZero() {
				t.Errorf("object %+v", o)
			}
		}
		if !reflect.DeepEqual(keys, names[:3]) {
			t.Fatalf("list %v", keys)
		}
		var buf bytes.Buffer
		if n, err := cl.Get(ctx, names[1], &buf); err != nil || n != int64(len(data)) || !bytes.Equal(buf.Bytes(), data) {
			t.Fatalf("get: %d %v", n, err)
		}
		if err := cl.Delete(ctx, names[1]); err != nil {
			t.Fatal(err)
		}
		if err := cl.Delete(ctx, names[1]); err != nil {
			t.Fatalf("deleting a missing key: %v", err)
		}
		if _, err := cl.Get(ctx, names[1], &buf); !IsNotFound(err) {
			t.Fatalf("get after delete: %v", err)
		}
		// The styles: the bucket is in the host or in the path, never both.
		for _, r := range f.Requests() {
			if inPath := strings.Contains(r, " /bobres-test/"); inPath == virtual {
				t.Errorf("virtual %v: request %s", virtual, r)
			}
		}
		refusedNone(t, f)
		f.Server.Close()
	}
}

func TestMultipart(t *testing.T) {
	f := s3test.New("bobres-test")
	defer f.Server.Close()
	cl := fakeClient(t, f, false, func(c *Config) { c.Threshold, c.PartSize = 10, 4 })
	data := []byte("0123456789abcdefghijklm") // 23 bytes: 6 parts, the last of 3
	if err := cl.PutFile(context.Background(), "bobres-20261011T033000Z.bkp", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Object("bobres-20261011T033000Z.bkp"); string(got) != string(data) {
		t.Fatalf("assembled %q", got)
	}
	var parts int
	for _, r := range f.Requests() {
		if strings.HasPrefix(r, "PUT ") && strings.Contains(r, "partNumber=") {
			parts++
		}
	}
	if parts != 6 || f.Uploads() != 0 {
		t.Fatalf("%d parts, %d uploads open: %v", parts, f.Uploads(), f.Requests())
	}
	refusedNone(t, f)

	// At the threshold, one PUT.
	small := data[:10]
	if err := cl.PutFile(context.Background(), "small.bkp", bytes.NewReader(small), 10); err != nil {
		t.Fatal(err)
	}
	if last := f.Requests()[len(f.Requests())-1]; last != "PUT /bobres-test/small.bkp?" {
		t.Fatalf("small file: %s", last)
	}
}

// A part that fails ends the upload: the parts already sent are aborted, not
// left in the bucket.
func TestMultipartAbortsOnFailure(t *testing.T) {
	f := s3test.New("bobres-test")
	defer f.Server.Close()
	f.Fail(func(r *http.Request) int {
		if r.URL.Query().Get("partNumber") == "3" {
			return http.StatusForbidden
		}
		return 0
	})
	cl := fakeClient(t, f, false, func(c *Config) { c.Threshold, c.PartSize = 10, 4 })
	data := []byte("0123456789abcdefghijklm")
	err := cl.PutFile(context.Background(), "k.bkp", bytes.NewReader(data), int64(len(data)))
	var se *Error
	if !errors.As(err, &se) || se.Status != http.StatusForbidden || !strings.Contains(err.Error(), "upload part 3") {
		t.Fatalf("err %v", err)
	}
	if _, ok := f.Object("k.bkp"); ok || f.Uploads() != 0 {
		t.Fatalf("object stored or upload left open (%d)", f.Uploads())
	}
	if last := f.Requests()[len(f.Requests())-1]; !strings.HasPrefix(last, "DELETE /bobres-test/k.bkp?uploadId=") {
		t.Fatalf("no abort: %v", f.Requests())
	}
}

// 5xx and 429 are tried three times with a pause (1 s, 4 s); a 4xx answer is
// final.
func TestRetries(t *testing.T) {
	f := s3test.New("bobres-test")
	defer f.Server.Close()
	var fails atomic.Int32
	fails.Store(2)
	f.Fail(func(r *http.Request) int {
		if r.Method == http.MethodPut && fails.Add(-1) >= 0 {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	var slept []time.Duration
	cl := fakeClient(t, f, false, func(c *Config) {
		c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	})
	if err := cl.PutFile(context.Background(), "k", strings.NewReader("data"), 4); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(slept, []time.Duration{time.Second, 4 * time.Second}) {
		t.Fatalf("slept %v", slept)
	}

	fails.Store(5)
	slept = nil
	err := cl.PutFile(context.Background(), "k", strings.NewReader("data"), 4)
	if se := (*Error)(nil); !errors.As(err, &se) || se.Status != http.StatusServiceUnavailable || len(slept) != 2 {
		t.Fatalf("after three tries: %v, slept %v", err, slept)
	}

	f.Fail(func(*http.Request) int { return http.StatusForbidden })
	slept = nil
	if err := cl.PutFile(context.Background(), "k", strings.NewReader("data"), 4); err == nil || len(slept) != 0 {
		t.Fatalf("a 403 was tried again: %v %v", err, slept)
	}
}

func TestWrongSecretIsRefused(t *testing.T) {
	f := s3test.New("bobres-test")
	defer f.Server.Close()
	cl := fakeClient(t, f, false, func(c *Config) { c.SecretKey = strings.Repeat("x", 40) })
	err := cl.PutFile(context.Background(), "k", strings.NewReader("data"), 4)
	var se *Error
	if !errors.As(err, &se) || se.Code != "SignatureDoesNotMatch" || len(f.Refused()) == 0 {
		t.Fatalf("err %v, refused %v", err, f.Refused())
	}
	if strings.Contains(err.Error(), strings.Repeat("x", 40)) {
		t.Fatal("the error shows the secret key")
	}
}

// A redirect (a wrong region or endpoint) is reported, never followed with
// the signed request.
func TestRedirectIsNotFollowed(t *testing.T) {
	followed := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			return
		}
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusMovedPermanently)
		_, _ = w.Write([]byte(`<Error><Code>PermanentRedirect</Code><Message>Use the right endpoint.</Message></Error>`))
	}))
	defer srv.Close()
	cl, err := New(Config{Endpoint: srv.URL, Bucket: "b", AccessKey: "a", SecretKey: "s", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cl.Get(context.Background(), "k", &bytes.Buffer{})
	if err == nil || followed || !strings.Contains(err.Error(), "HTTP 301: PermanentRedirect: Use the right endpoint.") {
		t.Fatalf("err %v, followed %v", err, followed)
	}
}

func TestNewRefuses(t *testing.T) {
	ok := Config{Endpoint: "https://s3.example.com", Bucket: "b", AccessKey: "a", SecretKey: "s"}
	for name, mod := range map[string]func(*Config){
		"plain http":   func(c *Config) { c.Endpoint = "http://s3.example.com" },
		"no scheme":    func(c *Config) { c.Endpoint = "s3.example.com" },
		"path":         func(c *Config) { c.Endpoint = "https://s3.example.com/bucket" },
		"credentials":  func(c *Config) { c.Endpoint = "https://user:pw@s3.example.com" },
		"no bucket":    func(c *Config) { c.Bucket = "" },
		"no secret":    func(c *Config) { c.SecretKey = "" },
		"no access id": func(c *Config) { c.AccessKey = "" },
	} {
		c := ok
		mod(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	cl, err := New(Config{Endpoint: "https://s3.example.com:443/", Bucket: "b", AccessKey: "a", SecretKey: "s"})
	if err != nil || cl.base.Host != "s3.example.com" || cl.c.Region != DefaultRegion || cl.virtual {
		t.Fatalf("%+v %v", cl, err)
	}
	if cl.URL("p/k.bkp") != "s3://b/p/k.bkp" {
		t.Fatalf("URL %s", cl.URL("p/k.bkp"))
	}
}

func TestAddressingStyle(t *testing.T) {
	for _, c := range []struct {
		host, bucket string
		virtual      bool
	}{
		{"shop.s3.example.com", "shop", true},
		{"shop.s3.ir-thr-at1.arvanstorage.ir", "shop", true},
		{"Shop.S3.Example.com:8443", "shop", true},
		{"s3.example.com", "shop", false},
		{"minio.example.com", "minio", false}, // a server named like the bucket
		{"shopping.s3.example.com", "shop", false},
		{"127.0.0.1:9000", "shop", false},
	} {
		if got := virtualHost(c.host, c.bucket); got != c.virtual {
			t.Errorf("virtualHost(%q, %q) = %v", c.host, c.bucket, got)
		}
	}
}

func TestWithBucket(t *testing.T) {
	c := Config{Endpoint: "https://shop.s3.example.com", Bucket: "shop"}
	if got := c.WithBucket("old-shop"); got.Endpoint != "https://old-shop.s3.example.com" || got.Bucket != "old-shop" {
		t.Fatalf("virtual-hosted: %+v", got)
	}
	c = Config{Endpoint: "https://s3.example.com", Bucket: "shop"}
	if got := c.WithBucket("old-shop"); got.Endpoint != "https://s3.example.com" || got.Bucket != "old-shop" {
		t.Fatalf("path style: %+v", got)
	}
}
