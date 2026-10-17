// Package search — mapping.go
//
// What the index keeps of each document (task #261, schema v4).
//
// Until v3 the index was created with Bleve's DEFAULT mapping, which keeps
// everything four ways: every field stored, indexed with its term vectors,
// copied into the `_all` composite field (with term vectors again), and kept
// a third time as doc values. Every one of those copies of `content` - up to
// 200 KiB of extracted text per file - was paid for, and none but two was
// ever read. Measured on an install with 21,000 documents: an 8 GB index
// (the issue #115 reporter, 2026-10-13).
//
// What filex actually reads, and so what each field keeps:
//
//	field        indexed  stored  term vectors  why
//	name         yes      yes     no            matched; read back to rank (runNameSearch)
//	path         yes      yes     no            matched (wildcard); read back to rank
//	name_norm    yes      no      no            matched (term, match, fuzzy)
//	path_norm    yes      no      no            matched
//	type         yes      no      no            the file/folder counts and filters
//	mime         yes      no      no            short; kept searchable for a later filter
//	storage_id   yes      no      no            a number; kept for a later filter
//	content      yes      yes     YES           matched; highlighted for the snippet,
//	                                            and carried across a rebuild
//	content_sig  no       yes     no            read back to skip re-extraction
//
// No field goes into `_all` (every query names its field), none keeps doc
// values (nothing sorts or facets inside Bleve: the ranking is computed in Go),
// and a field not listed here is not indexed at all.
//
// ⚠⚠ The ANALYSIS is unchanged: every text field uses the index's default
// analyzer ("standard"), exactly what the dynamic mapping gave it, so a query
// matches what it matched before - only the copies nobody read are gone. A
// custom analyzer here would make a rebuilt index answer differently from the
// one it replaces (see the note on `doc` in index.go).
//
// The mapping is stored inside the index when the index is created, so an
// existing index keeps its old one until it is rebuilt; the schema version
// (name.go, "4") is what makes the automatic repair rebuild it once.
package search

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

// indexMapping is the mapping every index this build creates is given: a new
// install's (Open) and a rebuild's replacement (rebuild.go).
func indexMapping() mapping.IndexMapping {
	field := func(kind string, index, store, vectors bool) *mapping.FieldMapping {
		var f *mapping.FieldMapping
		if kind == "number" {
			f = bleve.NewNumericFieldMapping()
		} else {
			f = bleve.NewTextFieldMapping()
		}
		f.Index = index
		f.Store = store
		f.IncludeTermVectors = vectors
		f.IncludeInAll = false
		f.DocValues = false
		return f
	}
	dm := bleve.NewDocumentStaticMapping()
	dm.AddFieldMappingsAt("name", field("text", true, true, false))
	dm.AddFieldMappingsAt("path", field("text", true, true, false))
	dm.AddFieldMappingsAt("name_norm", field("text", true, false, false))
	dm.AddFieldMappingsAt("path_norm", field("text", true, false, false))
	dm.AddFieldMappingsAt("type", field("text", true, false, false))
	dm.AddFieldMappingsAt("mime", field("text", true, false, false))
	dm.AddFieldMappingsAt("storage_id", field("number", true, false, false))
	dm.AddFieldMappingsAt("content", field("text", true, true, true))
	dm.AddFieldMappingsAt("content_sig", field("text", false, true, false))

	im := bleve.NewIndexMapping()
	im.DefaultMapping = dm
	im.IndexDynamic = false
	im.StoreDynamic = false
	im.DocValuesDynamic = false
	return im
}
