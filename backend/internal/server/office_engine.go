package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/onlyoffice"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// officeEngine is the apps' office engine (`engines:office`, alias
// `engines:libreoffice`) on the connected OnlyOffice Document Server: the
// shared conversion client (onlyoffice.Converter), the input put on offer for
// the one conversion (onlyoffice.OfferFile), the result fetched from the
// document server's own origin only and within the per-file limit.
type officeEngine struct {
	svc *onlyoffice.Service
}

var _ wasmplugin.OfficeConverter = officeEngine{}

// officeEngineParams names what an engine conversion asks for, in its key: a
// change here is a new key, never an old cached answer.
const officeEngineParams = "engine-v1"

func (o officeEngine) Ready(ctx context.Context) bool { return o.svc != nil && o.svc.EnabledCtx(ctx) }

func (o officeEngine) Convert(ctx context.Context, req wasmplugin.OfficeRequest) error {
	if o.svc == nil {
		return wasmplugin.ErrOfficeUnconfigured
	}
	conv, err := o.svc.Converter(ctx)
	if errors.Is(err, onlyoffice.ErrNotConfigured) {
		return wasmplugin.ErrOfficeUnconfigured
	}
	if err != nil {
		return &wasmplugin.OfficeError{Reason: err.Error()}
	}
	sum, err := fileSHA256(req.Src)
	if err != nil {
		return &wasmplugin.OfficeError{Reason: "reading the input: " + err.Error()}
	}
	fetch, withdraw, err := o.svc.OfferFile(ctx, req.Src, req.Name)
	if errors.Is(err, onlyoffice.ErrNotConfigured) {
		return wasmplugin.ErrOfficeUnconfigured
	}
	if err != nil {
		return &wasmplugin.OfficeError{Reason: err.Error()}
	}
	defer withdraw()
	// ⚠ A fresh key for every run. The document server keeps a key's answer
	// for a day, a failure included (lesson #935): a conversion that failed
	// once for a passing reason must not fail again from the cache. The
	// content and parameters are in it too, so two runs never share one.
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	key := onlyoffice.ConvertKey(onlyoffice.PurposeConvert, officeEngineParams, sum, req.From, req.To,
		strconv.Itoa(req.Delimiter), strconv.Itoa(req.CodePage), hex.EncodeToString(nonce[:]))
	res, err := conv.Convert(ctx, onlyoffice.ConvertRequest{
		FetchURL: fetch, FileType: req.From, OutputType: req.To, Title: req.Name, Key: key,
		CodePage: req.CodePage, Delimiter: req.Delimiter,
	})
	if err != nil {
		return officeFailure(ctx, err)
	}
	body, err := conv.FetchResult(ctx, res, req.MaxBytes)
	if err != nil {
		if errors.Is(err, onlyoffice.ErrResultTooLarge) {
			return wasmplugin.ErrOfficeTooLarge
		}
		return officeFailure(ctx, err)
	}
	f, err := os.OpenFile(req.Dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return &wasmplugin.OfficeError{Reason: "writing the result: " + err.Error()}
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = os.Remove(req.Dst)
		return &wasmplugin.OfficeError{Reason: "writing the result: " + err.Error()}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(req.Dst)
		return &wasmplugin.OfficeError{Reason: "writing the result: " + err.Error()}
	}
	return nil
}

// officeFailure turns the client's error into the engine's: the document
// server's own code when it gave one, the job's context when that is what
// ended it.
func officeFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if ce, ok := onlyoffice.AsConvertError(err); ok {
		return &wasmplugin.OfficeError{Code: ce.Code, Reason: ce.Error()}
	}
	return &wasmplugin.OfficeError{Reason: err.Error()}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
