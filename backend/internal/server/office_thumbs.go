package server

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// officeThumbs is the thumbnail pipeline's view of the OnlyOffice document
// server (thumb.OfficeDrawer): whether it is configured now, and drawing one
// document's first page through the shared conversion client
// (onlyoffice.Service.DrawThumbnail). The thumb package cannot import the
// onlyoffice one (onlyoffice → protocolsync → thumb); this is where they meet,
// as appThumbs is for the apps.
type officeThumbs struct {
	svc *onlyoffice.Service
}

func (o *officeThumbs) Ready(ctx context.Context) bool { return o.svc.EnabledCtx(ctx) }

func (o *officeThumbs) DrawPage(ctx context.Context, pg thumb.OfficePage) ([]byte, error) {
	png, err := o.svc.DrawThumbnail(ctx, onlyoffice.ThumbRequest{
		NodeID: pg.NodeID, StorageID: pg.StorageID, Name: pg.Name,
		ContentSig: pg.ContentSig, Attempt: pg.Attempt, MaxBytes: pg.MaxBytes,
	})
	if err == nil {
		return png, nil
	}
	return nil, officeError(err)
}

// officeError says a conversion failure in the pipeline's words: the class
// decides what the thumbnail row records (thumb/office.go, the table).
func officeError(err error) *thumb.OfficeError {
	if errors.Is(err, onlyoffice.ErrNotConfigured) {
		return &thumb.OfficeError{Class: thumb.OfficeUnconfigured, Err: err}
	}
	ce, ok := onlyoffice.AsConvertError(err)
	if !ok {
		return &thumb.OfficeError{Class: thumb.OfficeTransient, What: "failed", Err: err}
	}
	class := thumb.OfficeTransient
	switch ce.Class {
	case onlyoffice.ClassCorrupt:
		class = thumb.OfficeCorrupt
	case onlyoffice.ClassPassword:
		class = thumb.OfficePassword
	case onlyoffice.ClassTooLarge:
		class = thumb.OfficeTooLarge
	case onlyoffice.ClassEncrypted:
		class = thumb.OfficeEncrypted
	}
	return &thumb.OfficeError{Class: class, What: ce.What, Err: err}
}
