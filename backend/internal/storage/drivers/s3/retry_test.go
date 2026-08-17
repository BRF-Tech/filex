package s3

import (
	"errors"
	"net/http"
	"testing"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// A transient 503 from the object store must not sink a whole sync run.
//
// Real failure (fm.example.com, 12 occurrences over a month):
//
//	sync: run failed  storage=s3-test
//	err="s3: list: operation error S3: ListObjectsV2, exceeded maximum number
//	     of attempts, 3, https response error StatusCode: 503 …"
//
// The SDK default is 3 attempts, which is spent within a couple of seconds —
// too fast to outlast a brief upstream wobble. The run then aborts on a single
// listing page and the catalogue stays stale until the next scheduled pass.
//
// Two things have to hold for the wider budget to mean anything, so both are
// asserted: the attempt count itself, AND that a 503 is actually classified as
// retryable. Raising the count would be pointless if the error were treated as
// terminal.
func TestRetryerAbsorbsTransient503(t *testing.T) {
	r := newRetryer()

	if got := r.MaxAttempts(); got != 6 {
		t.Fatalf("MaxAttempts = %d, want 6 (SDK default 3 was too tight for a 503)", got)
	}

	if !r.IsErrorRetryable(responseErr(http.StatusServiceUnavailable)) {
		t.Fatal("503 ServiceUnavailable classified as terminal; the extra attempts would never be used")
	}
}

// A 403 is a credential/permission problem: retrying cannot fix it and burning
// six attempts only delays a clear failure. Guards against someone "fixing"
// flakiness later by marking everything retryable.
func TestRetryerDoesNotRetryPermissionFailures(t *testing.T) {
	if newRetryer().IsErrorRetryable(responseErr(http.StatusForbidden)) {
		t.Fatal("403 treated as retryable; permission failures must fail fast")
	}
}

func responseErr(status int) error {
	return &awshttp.ResponseError{
		ResponseError: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
			Err:      errors.New("upstream said so"),
		},
	}
}
