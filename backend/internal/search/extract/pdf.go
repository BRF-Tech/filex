package extract

import (
	"bytes"
	"context"
	"io"

	pdflib "github.com/ledongthuc/pdf"
)

func init() { Register(pdfExtractor{}) }

// pdfExtractor pulls the text layer out of a PDF via the pure-Go
// ledongthuc/pdf reader. Scanned/image-only PDFs have no text layer and
// yield "" — by contract that is NOT an error (OCR is a separate,
// optional extractor). Parse failures are treated the same way: a broken
// PDF is "nothing to extract", not a retryable job failure.
type pdfExtractor struct{}

func (pdfExtractor) Supports(mime, ext string) bool {
	return ext == "pdf" || mime == "application/pdf" || mime == "application/x-pdf"
}

func (pdfExtractor) Extract(_ context.Context, r io.Reader, limit int64) (text string, err error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	// The pdf reader needs io.ReaderAt + size, so buffer the source. The
	// caller caps source size (FILEX_SEARCH_CONTENT_MAX), keeping this
	// bounded.
	data, rerr := io.ReadAll(r)
	if rerr != nil {
		return "", rerr // transport failure — the queue may retry
	}
	// ledongthuc/pdf panics on some malformed inputs; recover to the
	// contract's "empty, no error".
	defer func() {
		if rec := recover(); rec != nil {
			text, err = "", nil
		}
	}()
	rd, perr := pdflib.NewReader(bytes.NewReader(data), int64(len(data)))
	if perr != nil {
		return "", nil
	}
	tr, terr := rd.GetPlainText()
	if terr != nil {
		return "", nil
	}
	b, _ := io.ReadAll(io.LimitReader(tr, limit))
	return clamp(sanitize(string(b)), limit), nil
}
