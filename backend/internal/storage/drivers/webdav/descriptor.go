package webdav

import (
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// Config contract for the webdav driver — every key Init reads, declared
// once so the admin form, the replication-target dialog, the CLI and
// storage.ValidateNonRootPath all agree. See storage/descriptor.go.
//
// "root" is new to the driver but not to the contract: the validator has
// always demanded it for webdav, the driver just never read it and the
// form never collected it, so the driver was uncreatable through the UI.
// Now both sides use this declaration; rows saved without a root keep
// mounting the base URL exactly as before.
func init() {
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  "webdav",
		Label:   "WebDAV",
		I18nKey: "storages.driver.webdav",
		Fields: append([]storage.Field{
			{
				Key:         "url",
				Type:        storage.FieldString,
				Label:       "Base URL",
				I18nKey:     "storages.fields.url",
				Placeholder: "https://dav.example.com/remote.php/dav/files/me/",
				Required:    true,
				Monospace:   true,
			},
			{
				Key:      "user",
				Type:     storage.FieldString,
				Label:    "User",
				I18nKey:  "storages.fields.user",
				Required: true,
				Aliases:  []string{"username"},
			},
			{
				Key:     "password",
				Type:    storage.FieldPassword,
				Label:   "Password",
				I18nKey: "storages.fields.password",
				Secret:  true,
			},
			{
				Key:         "root",
				Type:        storage.FieldString,
				Label:       "Base path",
				I18nKey:     "storages.fields.root",
				Help:        "Sub-folder under the base URL. Required: filex never takes ownership of the share root.",
				HelpI18nKey: "storages.fieldHelp.root",
				Placeholder: "fileman",
				Required:    true,
				Monospace:   true,
				Root:        true,
				Aliases:     []string{"base_path", "remote_path"},
			},
		}, defaults.Fields(stall.ServerTexts(attemptText))...),
	})
}

// attemptText is the attempt timeout's text on the WebDAV form: the shared
// one, plus the requests that wait longer for the server (timeout.go).
var attemptText = stall.FieldText{
	Label:       stall.AttemptText.Label,
	I18nKey:     stall.AttemptText.I18nKey,
	Help:        stall.AttemptText.Help + " Copies, moves and deletes wait up to 10 minutes for the answer, because the server answers them only when the work is done.",
	HelpI18nKey: "storages.fieldHelp.webdavAttemptTimeout",
}
