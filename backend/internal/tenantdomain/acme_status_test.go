package tenantdomain

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three things the screen says about an address under filex's own ACME:
// nothing yet, obtained until when, and not obtained with the authority's
// reason (docs/TENANT-ADMIN.md, "TLS").
func TestACMEStatus_ThreeStates(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	s := &ACMEStatus{Now: func() time.Time { return now }}

	assert.Equal(t, ACMENone, s.For("files.acme.example").State, "nothing asked yet")

	s.Problem("files.acme.example", "DNS problem: NXDOMAIN looking up A for files.acme.example")
	s.Failed("Files.Acme.Example.", errors.New(`acme/autocert: unable to satisfy "x" for domain "files.acme.example": no viable challenge type found`))
	r := s.For("files.acme.example")
	require.Equal(t, ACMEFailed, r.State)
	assert.Equal(t, "DNS problem: NXDOMAIN looking up A for files.acme.example", r.Reason,
		"the authority's reason, not autocert's 'no viable challenge type found'")
	require.NotNil(t, r.At)
	assert.Equal(t, now, *r.At)

	notAfter := now.Add(90 * 24 * time.Hour)
	now = now.Add(time.Minute)
	s.Obtained("files.acme.example", notAfter)
	r = s.For("files.acme.example")
	require.Equal(t, ACMEObtained, r.State)
	require.NotNil(t, r.NotAfter)
	assert.Equal(t, notAfter, *r.NotAfter)
	assert.Empty(t, r.Reason)
}

// A failure with no word from the authority (it could not be reached, its
// certificate is not trusted) keeps the error it ended on; an old reason from
// an earlier attempt does not stand for a new failure.
func TestACMEStatus_FailureWithoutTheAuthority(t *testing.T) {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	s := &ACMEStatus{Now: func() time.Time { return now }}

	s.Failed("files.acme.example", errors.New(`Get "https://ca.example/dir": tls: failed to verify certificate: x509: certificate signed by unknown authority`))
	assert.Contains(t, s.For("files.acme.example").Reason, "certificate signed by unknown authority")

	s.Problem("files.acme.example", "Connection refused")
	now = now.Add(problemFresh + time.Minute)
	s.Failed("files.acme.example", errors.New("context deadline exceeded"))
	assert.Equal(t, "context deadline exceeded", s.For("files.acme.example").Reason)
}

func TestACMEStatus_NilIsNothingYet(t *testing.T) {
	var s *ACMEStatus
	s.Obtained("x.example", time.Now())
	s.Failed("x.example", errors.New("x"))
	assert.Equal(t, ACMENone, s.For("x.example").State)
}
