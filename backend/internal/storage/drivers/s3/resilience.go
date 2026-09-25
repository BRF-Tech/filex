package s3

// How long the driver waits for a store that does not answer (issue #44).
//
// A drop into an S3 folder while Hetzner's object storage was down answered
// the right thing (503, "storage unavailable") after 85.6 s. Measured against
// the SDK defaults this file replaced: a refused port took 26 s, a host that
// dropped off the network 145 s, and a store that accepted the connection but
// never answered did not return at all (the SDK sets no response timeout).
//
// Three storage settings bound it now, all in the descriptor so the admin form
// draws them (descriptor.go):
//
//   - attempt_timeout_s: how long ONE attempt may wait for a sign of life —
//     connecting (DNS included), the TLS handshake, the answer after the
//     request is sent, and each next piece of the answer. It is never a limit
//     on a transfer that keeps moving: a 2 GB upload on a slow line takes as
//     long as it takes. An upload the store stops taking is cut after
//     stall.SendStallFloor (or the attempt timeout, if longer) — see there why.
//   - max_attempts: how many times a request is tried in all when the failure
//     can pass (network error, timeout, 5xx, throttling). A refusal — 403,
//     a missing bucket, a name that does not resolve, a bad certificate — is
//     never retried: asking again cannot change the answer.
//   - total_timeout_s: no new attempt starts unless a store that is still
//     down could show it within this many seconds of the first, so a store
//     that does not answer is given up on within it — except for the longer
//     waits below, which are on purpose. An attempt under way is not cut.
//
// The pieces that do the waiting are shared with the other network drivers
// (issue #73) and live in internal/storage/stall; what is here is how they are
// wired into the AWS SDK's middleware and retryer.
//
// ⚠ Server-side work is not silence. CopyObject (every rename is one) and
// CompleteMultipartUpload are answered only when the store has finished the
// work, which on MinIO means copying the whole object first. Those operations
// wait up to serverWorkTimeout for the answer; everything else waits the
// attempt timeout.
//
// ⚠ Why the answer is not waited for with http.Transport.ResponseHeaderTimeout:
// its clock starts when the request body is in the kernel's send buffer, not
// when it has left the machine, and it is one number for every operation. The
// per-attempt watch (stall.AnswerWatch) starts at the same moment but knows
// the operation and allows the buffered tail of an upload time to drain.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddle "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// defaults of the three settings (the bounds are stall's, shared by every
// driver). The descriptor reads the same values, so the form and the driver
// cannot disagree.
var defaults = stall.Defaults{AttemptTimeoutS: 10, MaxAttempts: 6, TotalTimeoutS: 15}

const serverWorkTimeout = 10 * time.Minute

// serverWorkOps are the operations the store answers only after doing the
// work (see the file comment).
var serverWorkOps = map[string]bool{
	"CopyObject":              true,
	"UploadPartCopy":          true,
	"CompleteMultipartUpload": true,
}

// testResolver lets a test point name lookups at a DNS server that never
// answers. nil in production: the system resolver.
var testResolver *net.Resolver

// policy is one storage's retry and timeout settings.
type policy struct{ stall.Policy }

// policyFrom reads the settings from a storage's config (stall.Settings says
// how a missing or out-of-bounds value is read).
func policyFrom(cfg map[string]any) policy {
	return policy{stall.Settings{
		AttemptTimeout: cfg["attempt_timeout_s"],
		MaxAttempts:    cfg["max_attempts"],
		TotalTimeout:   cfg["total_timeout_s"],
	}.Policy(defaults)}
}

func defaultPolicy() policy { return policyFrom(nil) }

// answerLimit is how long op may wait for the store's answer once the
// request is out, and for each next piece of it.
func (p policy) answerLimit(op string) time.Duration {
	if serverWorkOps[op] {
		return max(serverWorkTimeout, p.AttemptTimeout)
	}
	return p.AttemptTimeout
}

// httpClient builds the transport. Connecting (the name lookup included) and
// the TLS handshake are bounded by the attempt timeout, a send that stopped
// moving by SendStall; the answer is watched per attempt (stall.AnswerWatch).
func (p policy) httpClient() aws.HTTPClient {
	return awshttp.NewBuildableClient().
		WithDialerOptions(func(d *net.Dialer) {
			d.Timeout = p.AttemptTimeout
			if testResolver != nil {
				d.Resolver = testResolver
			}
		}).
		WithTransportOptions(func(tr *http.Transport) {
			tr.TLSHandshakeTimeout = p.AttemptTimeout
			tr.DialContext = stall.GuardWrites(tr.DialContext, p.SendStall())
		}).
		Freeze()
}

// apiOption installs the three middlewares that carry the policy.
func (p policy) apiOption(endpoint string) func(*middleware.Stack) error {
	return func(s *middleware.Stack) error {
		if err := s.Initialize.Add(middleware.InitializeMiddlewareFunc("FilexOperationClock", p.operation(endpoint)), middleware.After); err != nil {
			return err
		}
		if err := s.Finalize.Add(middleware.FinalizeMiddlewareFunc("FilexAttempt", p.attempt), middleware.After); err != nil {
			return err
		}
		return s.Deserialize.Add(middleware.DeserializeMiddlewareFunc("FilexBodyStallGuard", p.guardBody), middleware.After)
	}
}

type clockKey struct{}

type watchKey struct{}

// opClock is one operation's time account (stall.Clock) with the operation's
// name, which picks its answer limit.
type opClock struct {
	*stall.Clock
	op string
}

// operation starts the clock and, when the operation fails because the store
// is down, says so in words.
func (p policy) operation(endpoint string) func(context.Context, middleware.InitializeInput, middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
	return func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
		c := &opClock{Clock: stall.NewClock(p.Policy), op: awsmiddle.GetOperationName(ctx)}
		out, md, err := next.HandleInitialize(context.WithValue(ctx, clockKey{}, c), in)
		if err == nil || ctx.Err() != nil {
			return out, md, err // the caller gave up: its own error says why
		}
		if reason, down := p.Reason(err); down {
			cause := err
			var ae *attemptError
			if errors.As(err, &ae) {
				cause = ae.err
			}
			err = &stall.UnavailableError{What: "s3 endpoint " + endpoint, Reason: reason,
				Attempts: max(c.Attempts, 1), Took: time.Since(c.Began), Err: err, Cause: cause}
		}
		return out, md, err
	}
}

// attempt runs one attempt under an answer watch and tags a failure with the
// operation's clock, so the retryer — which the SDK hands the error but not
// the context — can hold the retries to the budget.
func (p policy) attempt(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
	c, _ := ctx.Value(clockKey{}).(*opClock)
	if c == nil {
		return next.HandleFinalize(ctx, in)
	}
	c.Attempts++
	var body int64
	if r, ok := in.Request.(*smithyhttp.Request); ok && r.Request != nil {
		body = r.ContentLength
	}
	w := stall.NewAnswerWatch(p.answerLimit(c.op) + stall.SendTail(body))
	started := time.Now()
	ctx = context.WithValue(httptrace.WithClientTrace(ctx, w.Trace()), watchKey{}, w)
	out, md, err := next.HandleFinalize(ctx, in)
	w.Stop()
	if err != nil {
		if w.Fired() {
			err = &stall.Error{Kind: stall.Answer, Limit: w.Limit(), Err: err}
		}
		c.Failed(started, err)
		err = &attemptError{err: err, clock: c}
	}
	return out, md, err
}

// guardBody is where the answer arrives: it stops the attempt's answer watch
// (⚠ here, not at the first response byte — see stall.AnswerWatch on
// "100 Continue"), and bounds a stalled answer body with stall.Reader.
func (p policy) guardBody(ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler) (middleware.DeserializeOutput, middleware.Metadata, error) {
	out, md, err := next.HandleDeserialize(ctx, in)
	if w, ok := ctx.Value(watchKey{}).(*stall.AnswerWatch); ok {
		w.Stop()
	}
	if resp, ok := out.RawResponse.(*smithyhttp.Response); ok && resp != nil && resp.Response != nil && resp.Body != nil && resp.Body != http.NoBody {
		resp.Body = stall.NewReader(resp.Body, p.answerLimit(awsmiddle.GetOperationName(ctx)))
	}
	return out, md, err
}

// attemptError carries one failed attempt's error with its operation's
// clock. It is transparent: the text and every errors.As/Is answer are the
// wrapped error's.
type attemptError struct {
	err   error
	clock *opClock
}

func (e *attemptError) Error() string { return e.err.Error() }
func (e *attemptError) Unwrap() error { return e.err }

func clockOf(err error) *opClock {
	var ae *attemptError
	if errors.As(err, &ae) {
		return ae.clock
	}
	return nil
}

// budgetRetryer is the SDK's standard retryer held to the time budget.
type budgetRetryer struct{ aws.RetryerV2 }

// newRetryer: the SDK's standard classification (network errors, timeouts,
// 5xx and throttling are retried; 403, 404, NXDOMAIN, TLS failures are not)
// and exponential backoff, with the attempt count from the policy and the
// time held to its budget.
func newRetryer(p policy) aws.Retryer {
	return budgetRetryer{retry.NewStandard(func(o *retry.StandardOptions) {
		o.MaxAttempts = p.MaxAttempts
		o.MaxBackoff = stall.MaxBackoff
		o.Retryables = append([]retry.IsErrorRetryable{retry.IsErrorRetryableFunc(refusedByTLS)}, o.Retryables...)
	})}
}

// refusedByTLS: the SDK counts every failed send as a retryable "connection
// error", so without this it spends the whole budget asking again for an
// answer a bad certificate cannot change (stall.RefusedByTLS).
func refusedByTLS(err error) aws.Ternary {
	if stall.RefusedByTLS(err) {
		return aws.FalseTernary
	}
	return aws.UnknownTernary
}

// GetRetryToken refuses another attempt once the budget has no room for one.
// Its error is what the operation returns, so it carries the attempt's error:
// "no more time" is not the reason the request failed.
func (r budgetRetryer) GetRetryToken(ctx context.Context, opErr error) (func(error) error, error) {
	if c := clockOf(opErr); c != nil && c.Room() < 0 {
		return nil, &retriesStoppedError{why: "retry time used up", err: opErr}
	}
	release, err := r.RetryerV2.GetRetryToken(ctx, opErr)
	if err != nil {
		return nil, &retriesStoppedError{why: "retries paused after many failures (" + err.Error() + ")", err: opErr}
	}
	return release, nil
}

// RetryDelay shortens the backoff so the next attempt still fits the budget.
func (r budgetRetryer) RetryDelay(attempt int, opErr error) (time.Duration, error) {
	d, err := r.RetryerV2.RetryDelay(attempt, opErr)
	if err != nil {
		return d, err
	}
	if c := clockOf(opErr); c != nil {
		d = max(min(d, c.Room()), 0)
	}
	return d, nil
}

type retriesStoppedError struct {
	why string
	err error
}

func (e *retriesStoppedError) Error() string { return e.why + ": " + e.err.Error() }
func (e *retriesStoppedError) Unwrap() error { return e.err }
