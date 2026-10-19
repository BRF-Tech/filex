// Package extracttest holds the documents the extraction tests read: one
// builder for every package that needs a real file (extract's own tests, the
// child process of extract/contain).
package extracttest

import (
	"bytes"
	"fmt"
	"strings"
)

// MinimalPDF assembles a syntactically valid single-page PDF whose content
// stream draws text - enough for the text-layer extractor.
func MinimalPDF(text string) []byte { return pagePDF(text, "") }

// NestedArraysPDF is a valid PDF whose page holds depth arrays one inside
// the other: the shape that sends ledongthuc/pdf's readArray/readObject into
// memory far beyond the file's size (GitHub #123). Measured with the real
// binary, a depth of 1,000,000 - a 2 MB file - took the parse to 908 MB.
func NestedArraysPDF(depth int) []byte {
	return pagePDF("nested", "/Annots "+strings.Repeat("[", depth)+strings.Repeat("]", depth)+" ")
}

// pagePDF is a one-page PDF drawing text, with extra written into the page's
// dictionary.
func pagePDF(text, extra string) []byte {
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R " + extra + "/Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n",
		fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(stream), stream),
		"5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = buf.Len()
		buf.WriteString(o)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return buf.Bytes()
}
