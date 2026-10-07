package storage

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Shaped returns the wrapper as the Driver the rest of filex is handed: one
// that has the primary's Toucher, Presigner and PartUploader exactly when the
// primary has them.
//
// ⚠ filex decides what a storage can do by TYPE-ASSERTING optional interfaces
// at some forty call sites (the reasoning of internal/plugin/gen). The bare
// *ReplicatedDriver has neither SetMtime nor the multipart calls, so wrapping
// a disk would silently drop the modification time an SFTP, FTP, NFS or S3
// client sends, and wrapping a bucket would turn every large upload into a
// single PUT; a wrapper that had them ALWAYS would make a storage without them
// claim abilities it lacks and fail at the last moment. Hence one type per
// combination (eight), picked here.
//
// What each carried interface does on the wrapper:
//   - Toucher: sets the time on the primary, then carries it to the replica
//     (best effort).
//   - Presigner: a download link is the primary's. An UPLOAD link is refused
//     (ErrUnsupported): the browser would write to the primary directly and the
//     fan-out would never see the file. No server path asks for one today.
//   - PartUploader: the parts go to the primary; CompleteMultipart fans the
//     assembled object out like a Write.
func (r *ReplicatedDriver) Shaped() Driver {
	_, t := r.primary.(Toucher)
	_, p := r.primary.(Presigner)
	_, m := r.primary.(PartUploader)
	to, pr, mp := touchOps{r: r}, presignOps{r: r}, multipartOps{r: r}
	switch {
	case t && p && m:
		return replDrvTPM{r, to, pr, mp}
	case t && p:
		return replDrvTP{r, to, pr}
	case t && m:
		return replDrvTM{r, to, mp}
	case p && m:
		return replDrvPM{r, pr, mp}
	case t:
		return replDrvT{r, to}
	case p:
		return replDrvP{r, pr}
	case m:
		return replDrvM{r, mp}
	default:
		return r
	}
}

type (
	replDrvT struct {
		*ReplicatedDriver
		touchOps
	}
	replDrvP struct {
		*ReplicatedDriver
		presignOps
	}
	replDrvM struct {
		*ReplicatedDriver
		multipartOps
	}
	replDrvTP struct {
		*ReplicatedDriver
		touchOps
		presignOps
	}
	replDrvTM struct {
		*ReplicatedDriver
		touchOps
		multipartOps
	}
	replDrvPM struct {
		*ReplicatedDriver
		presignOps
		multipartOps
	}
	replDrvTPM struct {
		*ReplicatedDriver
		touchOps
		presignOps
		multipartOps
	}
)

var (
	_ Driver       = (*ReplicatedDriver)(nil)
	_ Writer       = (*ReplicatedDriver)(nil)
	_ Deleter      = (*ReplicatedDriver)(nil)
	_ Mover        = (*ReplicatedDriver)(nil)
	_ Copier       = (*ReplicatedDriver)(nil)
	_ Mkdirer      = (*ReplicatedDriver)(nil)
	_ RangeReader  = (*ReplicatedDriver)(nil)
	_ Toucher      = replDrvT{}
	_ Presigner    = replDrvP{}
	_ PartUploader = replDrvM{}
	_ Toucher      = replDrvTP{}
	_ Presigner    = replDrvTP{}
	_ Toucher      = replDrvTM{}
	_ PartUploader = replDrvTM{}
	_ Presigner    = replDrvPM{}
	_ PartUploader = replDrvPM{}
	_ Toucher      = replDrvTPM{}
	_ Presigner    = replDrvTPM{}
	_ PartUploader = replDrvTPM{}
)

// touchOps carries the primary's Toucher.
type touchOps struct{ r *ReplicatedDriver }

// SetMtime sets the time on the primary, then on the replica (best effort).
func (o touchOps) SetMtime(ctx context.Context, p string, mtime time.Time) error {
	t, ok := o.r.primary.(Toucher)
	if !ok {
		return ErrUnsupported
	}
	if err := t.SetMtime(ctx, p, mtime); err != nil {
		return err
	}
	o.r.fanOutMtime(p, mtime)
	return nil
}

// presignOps carries the primary's Presigner, downloads only.
type presignOps struct{ r *ReplicatedDriver }

// PresignUpload is refused on a replicated storage: see Shaped.
func (o presignOps) PresignUpload(_ context.Context, p string, _ int64) (PresignedUpload, error) {
	return PresignedUpload{}, fmt.Errorf("%w: a direct upload to %q would skip the replication fan-out", ErrUnsupported, p)
}

// PresignDownload is the primary's.
func (o presignOps) PresignDownload(ctx context.Context, p string, ttl time.Duration) (string, error) {
	ps, ok := o.r.primary.(Presigner)
	if !ok {
		return "", ErrUnsupported
	}
	return ps.PresignDownload(ctx, p, ttl)
}

// multipartOps carries the primary's PartUploader; completing fans out.
type multipartOps struct{ r *ReplicatedDriver }

func (o multipartOps) part() (PartUploader, error) {
	pu, ok := o.r.primary.(PartUploader)
	if !ok {
		return nil, ErrUnsupported
	}
	return pu, nil
}

// InitMultipart is the primary's.
func (o multipartOps) InitMultipart(ctx context.Context, path string, totalSize int64, partCount int) (string, []string, error) {
	pu, err := o.part()
	if err != nil {
		return "", nil, err
	}
	return pu.InitMultipart(ctx, path, totalSize, partCount)
}

// UploadPart is the primary's.
func (o multipartOps) UploadPart(ctx context.Context, path, uploadID string, partNumber int, r io.Reader, size int64) (string, error) {
	pu, err := o.part()
	if err != nil {
		return "", err
	}
	return pu.UploadPart(ctx, path, uploadID, partNumber, r, size)
}

// CompleteMultipart assembles the object on the primary and fans it out.
func (o multipartOps) CompleteMultipart(ctx context.Context, path string, uploadID string, parts []PartCompletion) error {
	pu, err := o.part()
	if err != nil {
		return err
	}
	if err := pu.CompleteMultipart(ctx, path, uploadID, parts); err != nil {
		return err
	}
	o.r.afterWrite(path)
	return nil
}

// AbortMultipart is the primary's.
func (o multipartOps) AbortMultipart(ctx context.Context, path string, uploadID string) error {
	pu, err := o.part()
	if err != nil {
		return err
	}
	return pu.AbortMultipart(ctx, path, uploadID)
}

// UnavailableDriver stands in for a replica that could not be opened (its
// configuration is refused by the driver, its driver is not registered - a
// plugin that is not running). The storage keeps working; every change that
// would reach the backup is recorded as a REPLICA_UNAVAILABLE failure with
// err, so the problem is on the Replication page and in the bell instead of
// in a log line nobody reads, and Fix all replays the backlog once the target
// is repaired.
func UnavailableDriver(name string, err error) Driver {
	return &unavailableDriver{name: name, err: err}
}

type unavailableDriver struct {
	name string
	err  error
}

func (u *unavailableDriver) Init(context.Context, map[string]any) error { return nil }
func (u *unavailableDriver) Name() string                               { return u.name }
func (u *unavailableDriver) Capabilities() Capabilities                 { return Capabilities{} }
func (u *unavailableDriver) List(context.Context, string) ([]Object, error) {
	return nil, u.err
}
func (u *unavailableDriver) Stat(context.Context, string) (Object, error) { return Object{}, u.err }
func (u *unavailableDriver) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, u.err
}
