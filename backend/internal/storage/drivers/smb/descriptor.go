package smb

import "github.com/brf-tech/filex/backend/internal/storage"

func intp(v int) *int { return &v }

// Config contract for the smb driver — every key Init reads, declared once so
// the admin form, the replication-target dialog, the CLI and
// storage.ValidateNonRootPath all agree. See storage/descriptor.go.
func init() {
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  "smb",
		Label:   "SMB / CIFS (NAS)",
		I18nKey: "storages.driver.smb",
		Fields: []storage.Field{
			{
				Key:         "host",
				Type:        storage.FieldString,
				Label:       "Host",
				I18nKey:     "storages.fields.host",
				Placeholder: "nas.local",
				Required:    true,
				Monospace:   true,
			},
			{
				Key:     "port",
				Type:    storage.FieldInt,
				Label:   "Port",
				I18nKey: "storages.fields.port",
				Default: 445,
				Min:     intp(1),
				Max:     intp(65535),
			},
			{
				Key:         "share",
				Type:        storage.FieldString,
				Label:       "Share",
				I18nKey:     "storages.fields.share",
				Help:        "The share name alone, without the server: `media`, not `\\\\nas\\media`.",
				HelpI18nKey: "storages.fieldHelp.smbShare",
				Placeholder: "media",
				Required:    true,
				Monospace:   true,
				Aliases:     []string{"share_name"},
			},
			{
				Key:      "user",
				Type:     storage.FieldString,
				Label:    "User",
				I18nKey:  "storages.fields.user",
				Required: true,
				// Some NAS boxes still ship a guest share; the library refuses a
				// truly anonymous session, and `guest` is what it wants instead.
				Placeholder: "guest",
				Aliases:     []string{"username"},
			},
			{
				Key:     "password",
				Type:    storage.FieldPassword,
				Label:   "Password",
				I18nKey: "storages.fields.password",
				Secret:  true,
			},
			{
				Key:         "domain",
				Type:        storage.FieldString,
				Label:       "Domain / workgroup",
				I18nKey:     "storages.fields.domain",
				Help:        "Only for a Windows domain account. Leave empty for a NAS.",
				HelpI18nKey: "storages.fieldHelp.smbDomain",
				Advanced:    true,
				Aliases:     []string{"workgroup"},
			},
			{
				Key:         "root",
				Type:        storage.FieldString,
				Label:       "Base path",
				I18nKey:     "storages.fields.root",
				Help:        "Sub-folder inside the share. Leave empty for the whole share.",
				HelpI18nKey: "storages.fieldHelp.smbRoot",
				Placeholder: "projects",
				Monospace:   true,
				Root:        true,
				Aliases:     []string{"base_path", "path"},
			},
			{
				Key:      "dial_timeout_s",
				Type:     storage.FieldInt,
				Label:    "Connect timeout (seconds)",
				I18nKey:  "storages.fields.dialTimeout",
				Default:  15,
				Min:      intp(1),
				Max:      intp(300),
				Advanced: true,
			},
		},
	})
}
