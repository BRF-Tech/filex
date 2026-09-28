package handlers

// The rows apps add to the "New" menu (`new_documents`, internal/wasmplugin
// uinewdoc.go), as the document types the New-document dialog and the create
// path already know (internal/newdoc): the dialog lists them beside the
// built-in kinds, and planNewDoc makes one like any other — a file or a
// draft — from the app's template.

import (
	"github.com/brf-tech/filex/backend/internal/newdoc"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
)

// appNewDocType is an app's row as a document type.
func appNewDocType(d wasmplugin.AppNewDocument) newdoc.Type {
	return newdoc.Type{
		Ext: d.Ext, Group: newdoc.GroupApp, MIME: d.Mime, Requires: newdoc.RequiresApp, ExtRequired: true,
		Key: d.Key, App: &newdoc.AppDoc{Plugin: d.Plugin, View: d.View, Label: map[string]string(d.Label)},
	}
}

// AppNewDocTypes is every row the running apps add, as document types; nil
// without a registry.
func AppNewDocTypes(reg *wasmplugin.Registry) []newdoc.Type {
	if reg == nil {
		return nil
	}
	rows := reg.NewDocuments()
	out := make([]newdoc.Type, 0, len(rows))
	for _, d := range rows {
		out = append(out, appNewDocType(d))
	}
	return out
}
