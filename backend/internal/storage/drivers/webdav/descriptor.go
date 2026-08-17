package webdav

import "github.com/brf-tech/filex/backend/internal/storage"

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
		Fields: []storage.Field{
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
		},
	})
}
