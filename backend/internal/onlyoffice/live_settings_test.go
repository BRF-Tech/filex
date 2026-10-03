package onlyoffice

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Switched off is off. A Service wired to the live configuration answers from
// it alone: when the administrator switches OnlyOffice off (Live says
// nothing), the boot-time values from FILEX_ONLYOFFICE_URL / _JWT do not take
// over. Before 0.50 they did, so the editor, the office thumbnails and the
// apps' office engine went on using a document server that capabilities,
// About and the External services card said was off.
func TestSettings_TheLiveConfigurationIsTheOnlyAnswer(t *testing.T) {
	ctx := context.Background()
	svc := New(nil, nil, "http://boot-ds:8080/", "boot-secret", "http://filex:5212", 0)

	on := true
	svc.Live = func(context.Context) (string, string) {
		if on {
			return "http://live-ds/", "live-secret"
		}
		return "", ""
	}
	assert.True(t, svc.EnabledCtx(ctx), "configured live")
	assert.Equal(t, "http://live-ds", svc.LiveTarget(ctx).DocumentServerURL, "the live address, trimmed")
	assert.Equal(t, "live-secret", svc.LiveTarget(ctx).Secret)

	on = false
	assert.False(t, svc.EnabledCtx(ctx), "switched off live: the boot-time values must not answer instead")
	assert.Empty(t, svc.LiveTarget(ctx).DocumentServerURL)
	assert.Empty(t, svc.LiveTarget(ctx).Secret)

	// An address without a secret is not configured either (unchanged).
	svc.Live = func(context.Context) (string, string) { return "http://live-ds", "" }
	assert.False(t, svc.EnabledCtx(ctx))

	// No live source at all (a Service built for a test or a tool): the
	// values it was built with.
	svc.Live = nil
	assert.True(t, svc.EnabledCtx(ctx))
	assert.Equal(t, "http://boot-ds:8080", svc.LiveTarget(ctx).DocumentServerURL)
}
