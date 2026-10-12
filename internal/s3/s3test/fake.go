// Package s3test is an in-memory S3 server for tests: an httptest TLS server
// that checks every request's AWS Signature Version 4 (written here apart
// from package s3's signer, from the wire request: path and query as sent,
// the headers the signature names) and that the body matches its
// x-amz-content-sha256. It serves PUT, GET and DELETE of objects,
// ListObjectsV2 (in small pages, to exercise continuation), and multipart
// uploads, in path style or virtual-hosted style (Host bucket.<anything>).
package s3test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fake is the server. Its zero settings are filled by New.
type Fake struct {
	Server    *httptest.Server
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	// PageSize is how many keys one listing answers (default 2).
	PageSize int

	mu       sync.Mutex
	failFn   func(r *http.Request) int
	objects  map[string][]byte
	modified map[string]time.Time
	uploads  map[string]map[int][]byte
	nextID   int
	requests []string // "METHOD path?query" of every request served or refused
	bad      []string // why requests were refused (signature, payload)
}

// New starts a fake for bucket with low-entropy test credentials (never
// real ones). Close it with Server.Close.
func New(bucket string) *Fake {
	f := &Fake{
		Bucket: bucket, Region: "test-region-1",
		AccessKey: "TEST" + strings.Repeat("A", 16), SecretKey: strings.Repeat("s", 40),
		PageSize: 2,
		objects:  map[string][]byte{}, modified: map[string]time.Time{}, uploads: map[string]map[int][]byte{},
	}
	f.Server = httptest.NewTLSServer(f)
	return f
}

// URL is the path-style endpoint, https://127.0.0.1:<port>.
func (f *Fake) URL() string { return f.Server.URL }

// Client trusts the fake's certificate. With virtual set it dials the fake
// whatever the host, so https://<bucket>.s3.example.com:<port> reaches it (and
// checks the certificate as example.com's, the name it has).
func (f *Fake) Client(virtual bool) *http.Client {
	c := f.Server.Client()
	if !virtual {
		return c
	}
	tr := c.Transport.(*http.Transport).Clone()
	addr := f.Server.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tr.TLSClientConfig.ServerName = "example.com" // the test certificate's name
	return &http.Client{Transport: tr}
}

// VirtualURL is the virtual-hosted endpoint, https://<bucket>.s3.example.com:<port>.
func (f *Fake) VirtualURL() string {
	_, port, _ := net.SplitHostPort(f.Server.Listener.Addr().String())
	return "https://" + f.Bucket + ".s3.example.com:" + port
}

// Fail makes the fake answer a request with the status fn returns for it
// (0: serve it), with an S3 error document. nil serves every request.
func (f *Fake) Fail(fn func(r *http.Request) int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failFn = fn
}

// Put stores an object directly.
func (f *Fake) Put(key string, data []byte, modified time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = append([]byte(nil), data...)
	f.modified[key] = modified
}

// Object returns a stored object.
func (f *Fake) Object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.objects[key]
	return b, ok
}

// Keys are the stored keys, sorted.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Uploads is how many multipart uploads are still open (neither completed
// nor aborted).
func (f *Fake) Uploads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.uploads)
}

// Requests are the requests seen, as "METHOD path?query".
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// Refused says why requests were refused for their signature or payload
// hash; empty when every request was signed correctly.
func (f *Fake) Refused() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bad...)
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.fail(w, r, http.StatusBadRequest, "IncompleteBody", err.Error())
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	if why := f.verify(r, body); why != "" {
		f.mu.Lock()
		f.bad = append(f.bad, why)
		f.mu.Unlock()
		f.fail(w, r, http.StatusForbidden, "SignatureDoesNotMatch", why)
		return
	}
	f.mu.Lock()
	failFn := f.failFn
	f.mu.Unlock()
	if failFn != nil {
		if code := failFn(r); code != 0 {
			f.fail(w, r, code, "InternalError", "injected failure")
			return
		}
	}
	key, ok := f.key(r)
	if !ok {
		f.fail(w, r, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	}
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case key == "" && r.Method == http.MethodGet && q.Get("list-type") == "2":
		f.list(w, q)
	case key == "":
		f.fail(w, r, http.StatusNotImplemented, "NotImplemented", "bucket request")
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.nextID++
		id := fmt.Sprintf("upload-%d", f.nextID)
		f.uploads[id] = map[int][]byte{}
		writeXML(w, struct {
			XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
			Bucket   string
			Key      string
			UploadID string `xml:"UploadId"`
		}{Bucket: f.Bucket, Key: key, UploadID: id})
	case r.Method == http.MethodPut && q.Has("uploadId"):
		parts, ok := f.uploads[q.Get("uploadId")]
		n, err := strconv.Atoi(q.Get("partNumber"))
		if !ok || err != nil || n < 1 {
			f.fail(w, r, http.StatusNotFound, "NoSuchUpload", "no such upload")
			return
		}
		parts[n] = body
		sum := sha256.Sum256(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:16])+`"`)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		f.complete(w, r, key, q.Get("uploadId"), body)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(f.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		f.objects[key] = body
		f.modified[key] = time.Now().UTC()
		w.Header().Set("ETag", `"x"`)
	case r.Method == http.MethodGet:
		b, ok := f.objects[key]
		if !ok {
			f.fail(w, r, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		delete(f.modified, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		f.fail(w, r, http.StatusNotImplemented, "NotImplemented", r.Method)
	}
}

// key is the object key of r ("" for the bucket itself), in either style.
func (f *Fake) key(r *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if strings.HasPrefix(host, f.Bucket+".") {
		return strings.TrimPrefix(r.URL.Path, "/"), true
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/"+f.Bucket)
	if !ok || (rest != "" && rest[0] != '/') {
		return "", false
	}
	return strings.TrimPrefix(rest, "/"), true
}

func (f *Fake) list(w http.ResponseWriter, q url.Values) {
	prefix, after := q.Get("prefix"), q.Get("continuation-token")
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	type content struct {
		Key          string
		Size         int
		LastModified string
	}
	res := struct {
		XMLName               xml.Name `xml:"ListBucketResult"`
		Name                  string
		Prefix                string
		KeyCount              int
		IsTruncated           bool
		NextContinuationToken string `xml:",omitempty"`
		Contents              []content
	}{Name: f.Bucket, Prefix: prefix}
	for i, k := range keys {
		if i == f.PageSize {
			res.IsTruncated, res.NextContinuationToken = true, keys[i-1]
			break
		}
		res.Contents = append(res.Contents, content{Key: k, Size: len(f.objects[k]), LastModified: f.modified[k].Format(time.RFC3339)})
	}
	res.KeyCount = len(res.Contents)
	writeXML(w, res)
}

func (f *Fake) complete(w http.ResponseWriter, r *http.Request, key, id string, body []byte) {
	parts, ok := f.uploads[id]
	if !ok {
		f.fail(w, r, http.StatusNotFound, "NoSuchUpload", "no such upload")
		return
	}
	var doc struct {
		Parts []struct {
			PartNumber int
			ETag       string
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil || len(doc.Parts) == 0 {
		f.fail(w, r, http.StatusBadRequest, "MalformedXML", "bad part list")
		return
	}
	var all bytes.Buffer
	for i, p := range doc.Parts {
		data, ok := parts[p.PartNumber]
		sum := sha256.Sum256(data)
		if !ok || p.PartNumber != i+1 || p.ETag != `"`+hex.EncodeToString(sum[:16])+`"` {
			f.fail(w, r, http.StatusBadRequest, "InvalidPart", fmt.Sprintf("part %d", p.PartNumber))
			return
		}
		all.Write(data)
	}
	delete(f.uploads, id)
	f.objects[key] = all.Bytes()
	f.modified[key] = time.Now().UTC()
	writeXML(w, struct {
		XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
		Key     string
	}{Key: key})
}

// verify checks r's signature from what arrived. It returns why it does not
// hold ("" when it does).
func (f *Fake) verify(r *http.Request, body []byte) string {
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !ok {
		return "not a SigV4 Authorization header"
	}
	fields := map[string]string{}
	for _, p := range strings.Split(rest, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		fields[k] = v
	}
	cred := strings.Split(fields["Credential"], "/")
	stamp := r.Header.Get("X-Amz-Date")
	if len(cred) != 5 || cred[0] != f.AccessKey || cred[2] != f.Region || cred[3] != "s3" || cred[4] != "aws4_request" ||
		len(stamp) != 16 || cred[1] != stamp[:8] {
		return "wrong credential scope: " + strings.Join(cred[1:], "/")
	}
	payload := r.Header.Get("X-Amz-Content-Sha256")
	if payload != "UNSIGNED-PAYLOAD" {
		sum := sha256.Sum256(body)
		if payload != hex.EncodeToString(sum[:]) {
			return "the body does not match x-amz-content-sha256"
		}
	}
	signed := strings.Split(fields["SignedHeaders"], ";")
	var hdr strings.Builder
	for _, name := range signed {
		v := r.Header.Get(name)
		if name == "host" {
			v = r.Host
		}
		hdr.WriteString(name + ":" + strings.Join(strings.Fields(v), " ") + "\n")
	}
	for _, must := range []string{"host", "x-amz-date", "x-amz-content-sha256"} {
		if !contains(signed, must) {
			return "unsigned " + must
		}
	}
	// The query as sent, sorted by name then value (a wire query is already
	// in SigV4's encoding when the client is right).
	var q [][2]string
	if r.URL.RawQuery != "" {
		for _, p := range strings.Split(r.URL.RawQuery, "&") {
			k, v, _ := strings.Cut(p, "=")
			q = append(q, [2]string{k, v})
		}
	}
	sort.Slice(q, func(i, j int) bool {
		if q[i][0] != q[j][0] {
			return q[i][0] < q[j][0]
		}
		return q[i][1] < q[j][1]
	})
	pairs := make([]string, len(q))
	for i, p := range q {
		pairs[i] = p[0] + "=" + p[1]
	}
	canonical := strings.Join([]string{r.Method, r.URL.EscapedPath(), strings.Join(pairs, "&"), hdr.String(),
		fields["SignedHeaders"], payload}, "\n")
	scope := strings.Join(cred[1:], "/")
	h := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	k := mac([]byte("AWS4"+f.SecretKey), cred[1])
	for _, s := range cred[2:] {
		k = mac(k, s)
	}
	if want := hex.EncodeToString(mac(k, toSign)); !hmac.Equal([]byte(want), []byte(fields["Signature"])) {
		return "signature mismatch for " + r.Method + " " + r.URL.EscapedPath()
	}
	return ""
}

func (f *Fake) fail(w http.ResponseWriter, _ *http.Request, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string
		Message string
	}{Code: code, Message: msg})
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(v)
}

func mac(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
