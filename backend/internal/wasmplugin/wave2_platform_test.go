package wasmplugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// v0.43.0 wave 2 (2026-09-22): the manifest learns four things, and each is
// refused where it cannot mean anything.
func TestParseManifest_Wave2Fields(t *testing.T) {
	base := func(actions, extra string) string {
		return `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A","tr":"A"},"languages":["en","tr"],
		  "permissions":["files:read","files:write"],"actions":[` + actions + `]` + extra + `}`
	}
	ok := map[string]string{
		"a flow that ends in a write":    base(`{"id":"r","label":{"en":"R","tr":"R"},"applies":{"writable":true},"output":{"mode":"none"}}`, ""),
		"a result that may go elsewhere": base(`{"id":"c","label":{"en":"C","tr":"C"},"output":{"mode":"sibling","elsewhere":true}}`, ""),
		"a personal state rule":          base(`{"id":"f","label":{"en":"F","tr":"F"},"applies":{"state":["todo@me"]},"output":{"mode":"none"}}`, ""),
		"messages in every language":     base(`{"id":"x","label":{"en":"X","tr":"X"},"output":{"mode":"none"}}`, `,"messages":{"lock.held":{"en":"held for {who}","tr":"{who} için tutuluyor"}}`),
	}
	for name, doc := range ok {
		t.Run("accepts "+name, func(t *testing.T) {
			_, err := ParseManifest([]byte(doc))
			assert.NoError(t, err)
		})
	}
	bad := map[string]string{
		"writable without files:write":  `{"manifest_version":1,"name":"a","version":"1","label":{"en":"A"},"permissions":["files:read"],"actions":[{"id":"r","label":{"en":"R"},"applies":{"writable":true},"output":{"mode":"none"}}]}`,
		"elsewhere on a new version":    base(`{"id":"c","label":{"en":"C","tr":"C"},"output":{"mode":"version","elsewhere":true}}`, ""),
		"a folder declared up front":    base(`{"id":"c","label":{"en":"C","tr":"C"},"output":{"mode":"folder"}}`, ""),
		"a dir declared up front":       base(`{"id":"c","label":{"en":"C","tr":"C"},"output":{"mode":"sibling","dir":"docs://x"}}`, ""),
		"one person's number in a rule": base(`{"id":"f","label":{"en":"F","tr":"F"},"applies":{"state":["todo@7"]},"output":{"mode":"none"}}`, ""),
		"an @ with no key":              base(`{"id":"f","label":{"en":"F","tr":"F"},"applies":{"state":["@me"]},"output":{"mode":"none"}}`, ""),
		"a message without English":     base(`{"id":"x","label":{"en":"X","tr":"X"},"output":{"mode":"none"}}`, `,"messages":{"held":{"tr":"tutuluyor"}}`),
		"a message missing a language":  base(`{"id":"x","label":{"en":"X","tr":"X"},"output":{"mode":"none"}}`, `,"messages":{"held":{"en":"held"}}`),
		"a message key that is no id":   base(`{"id":"x","label":{"en":"X","tr":"X"},"output":{"mode":"none"}}`, `,"messages":{"Held Key":{"en":"held","tr":"tutuluyor"}}`),
	}
	for name, doc := range bad {
		t.Run("refuses "+name, func(t *testing.T) {
			_, err := ParseManifest([]byte(doc))
			assert.Error(t, err)
		})
	}
}

// A personal key reads `<key>@me` for its person and is gone for everybody
// else; every other key passes untouched.
func TestPersonalStateKeys(t *testing.T) {
	keys := []string{"sign:pending", "sign:todo@7", "sign:todo@12", "sign:mail@example.com", "sign:x@"}
	assert.Equal(t, []string{"sign:pending", "sign:todo@me", "sign:mail@example.com", "sign:x@"}, PersonalStateKeys(keys, 7))
	assert.Equal(t, []string{"sign:pending", "sign:todo@me", "sign:mail@example.com", "sign:x@"}, PersonalStateKeys(keys, 12))
	assert.Equal(t, []string{"sign:pending", "sign:mail@example.com", "sign:x@"}, PersonalStateKeys(keys, 3), "somebody with nothing to do sees no one's marker")
	assert.Equal(t, []string{"sign:pending", "sign:mail@example.com", "sign:x@"}, PersonalStateKeys(keys, 0), "nobody signed in is nobody's")
	assert.Equal(t, "todo@7", wire.PersonalState("todo", 7))
}

// The owner's rule for what depends on the server's setup: greyed with the
// reason for an administrator, hidden for everybody else. An action whose
// rule an engine would widen tells an ADMINISTRATOR what that engine would
// add; nobody else is told; an override the admin made is honoured; an
// engine the app was never granted is not the admin's to install.
func TestGated_WhatAMissingEngineWouldAdd(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, func(m map[string]any) {
		m["permissions"] = append(m["permissions"].([]any), "engines:libreoffice")
		for _, a := range m["actions"].([]any) {
			a := a.(map[string]any)
			switch a["id"] {
			case "upper":
				a["applies"] = map[string]any{"kind": "file", "ext": []any{"pdf"}, "engine_ext": map[string]any{"libreoffice": []any{"docx", "odt"}}}
			case "deliver":
				// imagemagick is NOT granted: not the administrator's to install.
				a["applies"] = map[string]any{"kind": "file", "ext": []any{"pdf"}, "engine_ext": map[string]any{"imagemagick": []any{"heic"}}}
			}
		}
	}))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)
	ctx := context.Background()

	gatedOf := func(admin bool, id string) []GatedRule {
		ans, err := h.reg.ActionsFor(ctx, admin)
		require.NoError(t, err)
		for _, a := range ans.Actions {
			if a.ID == id {
				return a.Gated
			}
		}
		t.Fatalf("no %s action", id)
		return nil
	}

	h.reg.engines = &engineSet{bins: map[string]string{}}
	assert.Equal(t, []GatedRule{{Ext: []string{"docx", "odt"}, Needs: Need{Kind: "engine", ID: "libreoffice", Name: "LibreOffice"}}},
		gatedOf(true, "upper"), "an administrator is told what LibreOffice would add, and by its name")
	assert.Nil(t, gatedOf(false, "upper"), "nobody else is")
	assert.Nil(t, gatedOf(true, "deliver"), "an engine the app was never granted is not offered for installing")

	// The admin removed .odt: only .docx would come with LibreOffice.
	require.NoError(t, h.reg.PutOverrides(ctx, p.Row.ID, []OverrideRow{
		{ID: "upper", Enabled: true, Applies: &wire.Applies{Kind: "file", Ext: []string{"pdf", "docx"}}},
	}))
	assert.Equal(t, []string{"docx"}, gatedOf(true, "upper")[0].Ext)

	// LibreOffice is here: nothing is missing.
	h.reg.engines = &engineSet{bins: map[string]string{"libreoffice": "/usr/bin/soffice"}}
	assert.Nil(t, gatedOf(true, "upper"))
}

// A lock reason named by a manifest message is kept as its key and made into
// words in every language the app wrote, each time somebody reads it; a key
// the manifest does not declare is refused; a plain reason stays plain.
func TestLockReason_AMessageInEveryLanguage(t *testing.T) {
	h := newHarness(t, nil)
	st, _, err := h.reg.Install(context.Background(), echoInput(t, nil))
	require.NoError(t, err)
	p, ok := h.reg.ByID(st.ID)
	require.True(t, ok)

	stored, err := lockMessage(p, "held", map[string]string{"who": "Ayşe"})
	require.NoError(t, err)
	assert.Equal(t, `msg:held {"who":"Ayşe"}`, stored)
	plain, text := h.reg.LockReason(&model.AppPluginLock{PluginName: "echo", Reason: stored})
	assert.Equal(t, "held for Ayşe", plain)
	assert.Equal(t, wire.Text{"en": "held for Ayşe", "tr": "Ayşe için tutuluyor"}, text)

	_, err = lockMessage(p, "nope", nil)
	assert.Error(t, err, "a key the manifest does not declare")

	plain, text = h.reg.LockReason(&model.AppPluginLock{PluginName: "echo", Reason: "under signature"})
	assert.Equal(t, "under signature", plain)
	assert.Nil(t, text)

	// The app is gone: no words at all, never the raw key.
	plain, text = h.reg.LockReason(&model.AppPluginLock{PluginName: "gone", Reason: stored})
	assert.Equal(t, "", plain)
	assert.Nil(t, text)
}
