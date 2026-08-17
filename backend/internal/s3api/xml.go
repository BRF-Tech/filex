package s3api

import (
	"encoding/xml"
	"log/slog"
	"net/http"
	"time"
)

// S3 speaks XML, and clients parse the ERROR CODE rather than the HTTP status.
// A correct refusal carrying the wrong code reads to a client as a broken
// server: rclone retries a NoSuchKey forever if it arrives as InternalError,
// and restic treats an unexpected code as repository corruption.

// Error is the body every failure returns.
type Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

// WriteError renders an S3 error. The message is for a human reading a log;
// the code is the contract.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	// ⚠ Some failures are decided AFTER the object headers are set (an
	// unsatisfiable Range is the one that bites): a stale Content-Length from
	// the object would then describe this XML body, and Go truncates the write
	// to match it. Clearing it here makes every error path safe regardless of
	// what ran before.
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(status)
	body := Error{Code: code, Message: message, Resource: r.URL.Path, RequestID: requestID(r)}
	if err := xml.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("s3api: encode error body", slog.Any("err", err))
	}
}

// WriteXML renders a successful XML response.
func WriteXML(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(status)
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return
	}
	if err := xml.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("s3api: encode body", slog.Any("err", err))
	}
}

// requestID echoes the client's trace id when it sent one, so a support
// conversation can match a client log line to a server log line.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Amz-Request-Id"); id != "" {
		return id
	}
	return r.Header.Get("X-Request-Id")
}

// ─────────────────────────── ListBuckets ───────────────────────────

// ListAllMyBucketsResult is the ListBuckets response.
type ListAllMyBucketsResult struct {
	XMLName xml.Name   `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListAllMyBucketsResult"`
	Owner   Owner      `xml:"Owner"`
	Buckets BucketList `xml:"Buckets"`
}

// BucketList wraps the bucket array. The nesting is not decoration: clients
// unmarshal <Buckets><Bucket/></Buckets> and a flattened list silently
// produces zero buckets rather than an error.
type BucketList struct {
	Bucket []Bucket `xml:"Bucket"`
}

// Bucket is one storage, seen as an S3 bucket.
type Bucket struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

// Owner identifies the caller. S3 requires the element; the values are opaque
// to every client we care about, so filex uses its own stable identifiers
// rather than inventing AWS-shaped ones.
type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

// amzTime formats a timestamp the way S3 does. ⚠ It is ISO-8601 in UTC with
// milliseconds — not RFC3339Nano, whose variable precision some clients parse
// as a different instant.
func amzTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
