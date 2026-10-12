// Package s3 is a small client for S3-compatible object storage (AWS S3,
// MinIO, Arvan Cloud, Backblaze B2's S3 API): the few requests the backup
// service and bobres need (put, multipart upload, list, get, delete), signed
// with AWS Signature Version 4 using only the standard library.
//
// Endpoints are https only. Every request is signed with the SHA-256 of its
// payload (never UNSIGNED-PAYLOAD), so the server refuses a body changed on
// the way. Two addressing styles: an endpoint whose host is the bucket name
// followed by a service host of three or more labels
// (https://mybucket.s3.example.com) is used as it is (virtual-hosted style);
// any other endpoint gets the bucket as the first path segment
// (https://s3.example.com/mybucket/key, path style). The three labels keep a
// server that merely shares its first label with the bucket
// (https://minio.example.com and a bucket named minio) in path style.
package s3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// MultipartThreshold is the largest file sent in one PUT; a larger one
	// goes up in parts.
	MultipartThreshold = 100 << 20
	// PartSize is the size of every part but the last (S3 takes 5 MiB to 5
	// GiB). It grows for a file that would need more than maxParts.
	PartSize = 16 << 20
	maxParts = 10000
	// DefaultRegion signs requests when no region is set (MinIO's default;
	// most S3-compatible services accept it).
	DefaultRegion = "us-east-1"
	// EmptySHA256 is the payload hash of a request without a body.
	EmptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	amzDateLayout = "20060102T150405Z"
	attempts      = 3
)

// Config is how a Client reaches a bucket.
type Config struct {
	// Endpoint is https://host[:port], with no path.
	Endpoint string
	Bucket   string
	// Region signs the requests (DefaultRegion when empty).
	Region    string
	AccessKey string
	SecretKey string

	// HTTPClient sends the requests (nil: one with no overall timeout:
	// the caller's context bounds each call). Redirects are never followed.
	HTTPClient *http.Client
	// Now is the signing clock (nil: time.Now).
	Now func() time.Time
	// Sleep waits between attempts (nil: a timer that ends with the context).
	Sleep func(ctx context.Context, d time.Duration) error
	// Threshold and PartSize replace MultipartThreshold and PartSize (tests).
	Threshold, PartSize int64
}

// WithBucket is c for another bucket on the same service: a virtual-hosted
// endpoint (one that names c's bucket) is renamed to the new bucket.
func (c Config) WithBucket(bucket string) Config {
	if u, err := url.Parse(c.Endpoint); err == nil && c.Bucket != "" && virtualHost(u.Host, c.Bucket) {
		u.Host = bucket + u.Host[len(c.Bucket):]
		c.Endpoint = u.String()
	}
	c.Bucket = bucket
	return c
}

// virtualHost reports whether host (with any port) is bucket.<service host>
// with a service host of three or more labels.
func virtualHost(host, bucket string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	rest, ok := strings.CutPrefix(strings.ToLower(host), strings.ToLower(bucket)+".")
	return ok && bucket != "" && strings.Count(rest, ".") >= 2
}

// Client sends signed requests to one bucket.
type Client struct {
	c       Config
	base    url.URL // scheme and host of the endpoint
	virtual bool    // the endpoint's host names the bucket
	hc      *http.Client
}

// New checks c and returns its Client.
func New(c Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(c.Endpoint))
	switch {
	case err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "":
		return nil, errors.New("s3: the endpoint must be an https URL (https://s3.example.com) without credentials")
	case u.Path != "" && u.Path != "/":
		return nil, errors.New("s3: the endpoint must not have a path (https://s3.example.com)")
	case c.Bucket == "":
		return nil, errors.New("s3: no bucket")
	case c.AccessKey == "" || c.SecretKey == "":
		return nil, errors.New("s3: no access key or secret key")
	}
	if c.Region == "" {
		c.Region = DefaultRegion
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Sleep == nil {
		c.Sleep = sleepCtx
	}
	if c.Threshold <= 0 {
		c.Threshold = MultipartThreshold
	}
	if c.PartSize <= 0 {
		c.PartSize = PartSize
	}
	// https://host:443 is https://host: the Host header and the signature
	// must agree on one spelling.
	host := u.Host
	if h, p, err := net.SplitHostPort(host); err == nil && p == "443" {
		host = h
		if strings.Contains(h, ":") {
			host = "[" + h + "]"
		}
	}
	hc := &http.Client{}
	if c.HTTPClient != nil {
		cp := *c.HTTPClient
		hc = &cp
	}
	// A redirect (a wrong region, a moved bucket) is reported as S3 sent it:
	// a signed request is never sent on to another place.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		c:       c,
		base:    url.URL{Scheme: "https", Host: host},
		virtual: virtualHost(host, c.Bucket),
		hc:      hc,
	}, nil
}

// Bucket is the client's bucket.
func (cl *Client) Bucket() string { return cl.c.Bucket }

// Host is the endpoint's host (and port, when not 443).
func (cl *Client) Host() string { return cl.base.Host }

// URL is the s3:// name of key: s3://bucket/key.
func (cl *Client) URL(key string) string { return "s3://" + cl.c.Bucket + "/" + key }

// Error is an answer from the storage service that is not a success.
type Error struct {
	Status  int
	Code    string // AccessDenied, NoSuchBucket, SignatureDoesNotMatch...
	Message string
}

func (e *Error) Error() string {
	var detail []string
	for _, s := range []string{e.Code, e.Message} {
		if s = strings.TrimSpace(s); s != "" {
			detail = append(detail, s)
		}
	}
	if len(detail) == 0 {
		detail = append(detail, http.StatusText(e.Status))
	}
	return fmt.Sprintf("the storage answered HTTP %d: %s", e.Status, shorten(strings.Join(detail, ": "), 300))
}

// IsNotFound reports whether err is a missing key or bucket.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusNotFound || e.Code == "NoSuchKey")
}

// PutFile stores size bytes of r as key: one PUT up to the multipart
// threshold, a multipart upload above it (aborted when a part fails, so no
// parts are left to pay for).
func (cl *Client) PutFile(ctx context.Context, key string, r io.ReaderAt, size int64) error {
	if size <= cl.c.Threshold {
		return cl.putObject(ctx, key, r, size)
	}
	return cl.multipart(ctx, key, r, size)
}

func (cl *Client) putObject(ctx context.Context, key string, r io.ReaderAt, size int64) error {
	sum, err := sectionSHA256(r, 0, size)
	if err != nil {
		return fmt.Errorf("s3: read the file: %w", err)
	}
	resp, err := cl.do(ctx, http.MethodPut, cl.objectPath(key), nil, sectionBody(r, 0, size), size, sum)
	if err != nil {
		return err
	}
	return drain(resp)
}

func (cl *Client) multipart(ctx context.Context, key string, r io.ReaderAt, size int64) (err error) {
	part := cl.c.PartSize
	if n := (size + part - 1) / part; n > maxParts {
		part = (size + maxParts - 1) / maxParts
		part = (part + 1<<20 - 1) &^ (1<<20 - 1) // whole MiB
	}
	path := cl.objectPath(key)
	resp, err := cl.do(ctx, http.MethodPost, path, url.Values{"uploads": {""}}, nil, 0, EmptySHA256)
	if err != nil {
		return fmt.Errorf("start the multipart upload: %w", err)
	}
	var started struct {
		UploadID string `xml:"UploadId"`
	}
	if err := decodeXML(resp, &started); err != nil {
		return fmt.Errorf("start the multipart upload: %w", err)
	}
	if started.UploadID == "" {
		return errors.New("start the multipart upload: no upload id in the answer")
	}
	id := started.UploadID
	defer func() {
		if err != nil {
			// The upload is abandoned: its parts would otherwise be kept (and
			// billed) until a lifecycle rule removes them.
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if resp, aerr := cl.do(actx, http.MethodDelete, path, url.Values{"uploadId": {id}}, nil, 0, EmptySHA256); aerr == nil {
				_ = drain(resp)
			}
		}
	}()

	type donePart struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}
	var parts []donePart
	for n, off := 1, int64(0); off < size; n, off = n+1, off+part {
		length := min(part, size-off)
		sum, err := sectionSHA256(r, off, length)
		if err != nil {
			return fmt.Errorf("s3: read the file: %w", err)
		}
		q := url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {id}}
		resp, err := cl.do(ctx, http.MethodPut, path, q, sectionBody(r, off, length), length, sum)
		if err != nil {
			return fmt.Errorf("upload part %d: %w", n, err)
		}
		etag := resp.Header.Get("ETag")
		_ = drain(resp)
		if etag == "" {
			return fmt.Errorf("upload part %d: the storage gave no ETag", n)
		}
		parts = append(parts, donePart{PartNumber: n, ETag: etag})
	}

	body, err := xml.Marshal(struct {
		XMLName xml.Name   `xml:"CompleteMultipartUpload"`
		Parts   []donePart `xml:"Part"`
	}{Parts: parts})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	resp, err = cl.do(ctx, http.MethodPost, path, url.Values{"uploadId": {id}}, bytesBody(body), int64(len(body)), hex.EncodeToString(sum[:]))
	if err != nil {
		return fmt.Errorf("complete the multipart upload: %w", err)
	}
	// S3 may answer 200 and still fail: the error is then in the body.
	var done struct {
		XMLName xml.Name
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := decodeXML(resp, &done); err != nil {
		return fmt.Errorf("complete the multipart upload: %w", err)
	}
	if done.XMLName.Local == "Error" {
		return fmt.Errorf("complete the multipart upload: %w", &Error{Status: resp.StatusCode, Code: done.Code, Message: done.Message})
	}
	return nil
}

// Object is one entry of a listing.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// List returns every object whose key begins with prefix, in key order.
func (cl *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for page := 0; page < 10000; page++ {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := cl.do(ctx, http.MethodGet, cl.bucketPath(), q, nil, 0, EmptySHA256)
		if err != nil {
			return nil, err
		}
		var res struct {
			Contents []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		if err := decodeXML(resp, &res); err != nil {
			return nil, fmt.Errorf("s3: list: %w", err)
		}
		for _, o := range res.Contents {
			out = append(out, Object{Key: o.Key, Size: o.Size, LastModified: o.LastModified})
		}
		if !res.IsTruncated || res.NextContinuationToken == "" || res.NextContinuationToken == token {
			sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
			return out, nil
		}
		token = res.NextContinuationToken
	}
	return nil, errors.New("s3: list: too many pages")
}

// Delete removes key; a key that is already gone is not an error.
func (cl *Client) Delete(ctx context.Context, key string) error {
	resp, err := cl.do(ctx, http.MethodDelete, cl.objectPath(key), nil, nil, 0, EmptySHA256)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return drain(resp)
}

// Get writes the object key to w and returns its size. A body shorter than
// the length the storage announced is an error.
func (cl *Client) Get(ctx context.Context, key string, w io.Writer) (int64, error) {
	resp, err := cl.do(ctx, http.MethodGet, cl.objectPath(key), nil, nil, 0, EmptySHA256)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck // read to the end below
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return n, fmt.Errorf("s3: download: %w", err)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return n, fmt.Errorf("s3: download: got %d of %d bytes", n, resp.ContentLength)
	}
	return n, nil
}

func (cl *Client) objectPath(key string) string {
	if cl.virtual {
		return "/" + key
	}
	return "/" + cl.c.Bucket + "/" + key
}

func (cl *Client) bucketPath() string {
	if cl.virtual {
		return "/"
	}
	return "/" + cl.c.Bucket + "/"
}

// do sends a signed request, up to three times while the storage cannot be
// reached or answers 5xx or 429. body makes a fresh body for each attempt
// (nil: none). It returns the response of a 2xx answer, to be closed.
func (cl *Client) do(ctx context.Context, method, path string, q url.Values, body func() io.Reader, size int64, payloadSHA string) (*http.Response, error) {
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			if err := cl.c.Sleep(ctx, time.Duration(1<<(2*(i-1)))*time.Second); err != nil { // 1s, 4s
				return nil, last
			}
		}
		req, err := cl.request(ctx, method, path, q, body, size, payloadSHA)
		if err != nil {
			return nil, err
		}
		resp, err := cl.hc.Do(req)
		if err != nil {
			last = fmt.Errorf("s3: %s: %w", method, err)
			if ctx.Err() != nil {
				return nil, last
			}
			continue
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		last = readError(resp)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, last
		}
	}
	return nil, last
}

// request builds and signs one request. The path and query are sent exactly
// as they are signed: each path segment and query part percent-encoded the
// way SigV4 encodes them, the query sorted.
func (cl *Client) request(ctx context.Context, method, path string, q url.Values, body func() io.Reader, size int64, payloadSHA string) (*http.Request, error) {
	u := cl.base
	u.Path = path
	u.RawPath = encodePath(path)
	u.RawQuery = canonicalQuery(q)
	var rd io.Reader = http.NoBody
	if body != nil && size > 0 {
		rd = body()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	cl.sign(req, payloadSHA, cl.c.Now())
	return req, nil
}

// sign adds the SigV4 headers (X-Amz-Date, X-Amz-Content-Sha256 and
// Authorization) to req. Every header already set on req is signed, with host.
func (cl *Client) sign(req *http.Request, payloadSHA string, t time.Time) {
	stamp := t.UTC().Format(amzDateLayout)
	day := stamp[:8]
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadSHA)
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	values := map[string]string{"host": host}
	for k, vs := range req.Header {
		name := strings.ToLower(k)
		if name == "authorization" {
			continue
		}
		vals := make([]string, len(vs))
		for i, v := range vs {
			vals[i] = strings.Join(strings.Fields(v), " ")
		}
		values[name] = strings.Join(vals, ",")
	}
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	var headers strings.Builder
	for _, n := range names {
		headers.WriteString(n + ":" + values[n] + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{
		req.Method, encodePath(req.URL.Path), canonicalQuery(req.URL.Query()), headers.String(), signed, payloadSHA,
	}, "\n")
	scope := day + "/" + cl.c.Region + "/s3/aws4_request"
	h := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(h[:])
	key := signingKey(cl.c.SecretKey, day, cl.c.Region, "s3")
	sig := hex.EncodeToString(hmacSHA256(key, []byte(toSign)))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cl.c.AccessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

// signingKey derives the SigV4 key of a day, region and service.
func signingKey(secret, day, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(day))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// encodePath percent-encodes a path as SigV4 wants it: every byte but
// A-Z a-z 0-9 - _ . ~ and the slashes.
func encodePath(p string) string {
	if p == "" {
		return "/"
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = uriEncode(s)
	}
	return strings.Join(parts, "/")
}

// canonicalQuery is the query sorted by name, then value, each encoded.
func canonicalQuery(q url.Values) string {
	type kv struct{ k, v string }
	var all []kv
	for k, vs := range q {
		for _, v := range vs {
			all = append(all, kv{uriEncode(k), uriEncode(v)})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].k != all[j].k {
			return all[i].k < all[j].k
		}
		return all[i].v < all[j].v
	})
	parts := make([]string, len(all))
	for i, p := range all {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

func uriEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}

func sectionSHA256(r io.ReaderAt, off, n int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r, off, n)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sectionBody(r io.ReaderAt, off, n int64) func() io.Reader {
	return func() io.Reader { return io.NewSectionReader(r, off, n) }
}

func bytesBody(b []byte) func() io.Reader {
	return func() io.Reader { return bytes.NewReader(b) }
}

// readError reads a failed answer's S3 error document (Code, Message) and
// closes it.
func readError(resp *http.Response) error {
	defer resp.Body.Close() //nolint:errcheck // read-only
	e := &Error{Status: resp.StatusCode}
	var doc struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)); err == nil && xml.Unmarshal(b, &doc) == nil {
		e.Code, e.Message = doc.Code, doc.Message
	}
	return e
}

func decodeXML(resp *http.Response, v any) error {
	defer resp.Body.Close() //nolint:errcheck // read-only
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	return xml.Unmarshal(b, v)
}

func drain(resp *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.Body.Close()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
