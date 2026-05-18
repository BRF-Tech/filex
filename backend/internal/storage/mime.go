package storage

import (
	"path/filepath"
	"strings"
)

// RefineOfficeMime upgrades the generic "application/zip" detected by
// http.DetectContentType to the registered OOXML/ODF MIME when the file
// has an office extension. Returns the input unchanged otherwise.
//
// Drivers and serving handlers call this BEFORE returning a stored or
// freshly-sniffed MIME to clients. The motivation: OnlyOffice Document
// Server refuses pptx/docx/odt sources when the fetch Content-Type is
// "application/zip" even though the JWT-signed config says
// fileType=pptx. xlsx happens to be lenient; the rest aren't.
func RefineOfficeMime(detected, filename string) string {
	if detected != "application/zip" {
		return detected
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".odp":
		return "application/vnd.oasis.opendocument.presentation"
	}
	return detected
}
