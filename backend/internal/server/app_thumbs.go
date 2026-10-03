package server

import (
	"context"
	"time"

	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// appThumbs is the thumbnail pipeline's view of the apps that draw
// thumbnails (thumb.AppThumbs): who is in a kind's chain comes from the
// Default apps rules (internal/assoc), and drawing is the app registry's.
// The two packages do not know each other; this is where they meet.
type appThumbs struct {
	assoc *assoc.Service
	reg   *wasmplugin.Registry
}

func (a *appThumbs) ThumbChain(ctx context.Context, name, mime string) assoc.Chain {
	return a.assoc.Chain(ctx, assoc.CapThumbnail, name, mime)
}

func (a *appThumbs) DrawThumbnail(ctx context.Context, h assoc.Handler, req assoc.DrawRequest) ([]byte, error) {
	return a.reg.DrawThumbnail(ctx, h.App, req)
}

func (a *appThumbs) AppLimits(app string) (int64, time.Duration, bool) {
	return a.reg.AppLimits(app)
}
