package officesave

// The names an office save takes when it is not written over its file
// (beside.go): one rule for every editor, the desktop app's too.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConflictName(t *testing.T) {
	at := time.Date(2026, 10, 6, 13, 15, 0, 0, time.FixedZone("TRT", 3*3600))
	assert.Equal(t, "Bütçe Özeti.filex-conflict-20261006T101500.xlsx", ConflictName("Bütçe Özeti.xlsx", at), "UTC, like the desktop's")
	assert.Equal(t, "README.filex-conflict-20261006T101500", ConflictName("README", at))
}

func TestIsConflictName(t *testing.T) {
	at := time.Date(2026, 10, 6, 13, 15, 0, 0, time.UTC)
	assert.True(t, IsConflictName(ConflictName("teklif.docx", at)))
	assert.True(t, IsConflictName("teklif.filex-conflict-20261006T131500 (2).docx"), "numbered when the name was taken")
	assert.True(t, IsConflictName(ConflictName("README", at)))
	assert.False(t, IsConflictName("teklif.docx"))
	assert.False(t, IsConflictName("rapor (2).docx"))
	assert.False(t, IsConflictName("my.filex-conflict-notes.docx"), "no time: a name somebody typed")
}

// A save kept beside a document is a zip package (OOXML, ODF), whichever
// editor sent it.
func TestBesideBytesOK(t *testing.T) {
	assert.True(t, BesideBytesOK([]byte("PK\x03\x04rest")))
	assert.False(t, BesideBytesOK([]byte("plain text")))
	assert.False(t, BesideBytesOK([]byte{0xD0, 0xCF, 0x11, 0xE0}), "a Word 97 file is not what a save beside is")
	assert.False(t, BesideBytesOK(nil))
}

func TestBesideName(t *testing.T) {
	assert.Equal(t, "/Raporlar/rapor.docx", BesideName("/Raporlar/rapor.doc", "docx"))
	assert.Equal(t, "/liste.xlsx", BesideName("/liste.xls", "xlsx"))
	assert.Equal(t, "/a.b/README.odt", BesideName("/a.b/README", "odt"), "a name without an extension gets one")
}

func TestDocExtFormatNameIsPackage(t *testing.T) {
	assert.Equal(t, "csv", DocExt("Liste.CSV"))
	assert.Equal(t, "", DocExt("README"))
	assert.Equal(t, "DOCX", FormatName("docx"))
	assert.True(t, IsPackage([]byte("PK\x03\x04 an XLSX")))
	assert.True(t, IsPackage([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1, 0}))
	assert.False(t, IsPackage([]byte("ad;adet\n")))
}
