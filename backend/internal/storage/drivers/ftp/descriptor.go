package ftp

import "github.com/brf-tech/filex/backend/internal/storage"

func intp(v int) *int { return &v }

// Config contract for the ftp driver — every key Init reads, declared once
// so the admin form, the replication-target dialog, the CLI and
// storage.ValidateNonRootPath all agree. See storage/descriptor.go.
//
// ftp has been registered in the backend since it was written but the
// admin form's hardcoded driver list never mentioned it, so the feature
// was invisible. The picker now renders from the registry.
func init() {
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  "ftp",
		Label:   "FTP / FTPS",
		I18nKey: "storages.driver.ftp",
		Fields: []storage.Field{
			{
				Key:         "host",
				Type:        storage.FieldString,
				Label:       "Host",
				I18nKey:     "storages.fields.host",
				Placeholder: "ftp.example.com",
				Required:    true,
				Monospace:   true,
			},
			{
				Key:     "port",
				Type:    storage.FieldInt,
				Label:   "Port",
				I18nKey: "storages.fields.port",
				Default: 21,
				Min:     intp(1),
				Max:     intp(65535),
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
				Key:      "password",
				Type:     storage.FieldPassword,
				Label:    "Password",
				I18nKey:  "storages.fields.password",
				Secret:   true,
				Required: true,
			},
			{
				Key:         "root",
				Type:        storage.FieldString,
				Label:       "Base path",
				I18nKey:     "storages.fields.root",
				Help:        "Sub-folder on the remote host. Required: filex never mounts the account root.",
				HelpI18nKey: "storages.fieldHelp.root",
				Placeholder: "/files",
				Required:    true,
				Monospace:   true,
				Root:        true,
				Aliases:     []string{"base_path", "remote_path"},
			},
			{
				Key:         "tls",
				Type:        storage.FieldBool,
				Label:       "FTPS (explicit AUTH TLS)",
				I18nKey:     "storages.fields.tls",
				Default:     false,
				Help:        "Plain FTP sends credentials in the clear — turn this on whenever the server supports it.",
				HelpI18nKey: "storages.fieldHelp.tls",
			},
			{
				Key:         "passive",
				Type:        storage.FieldBool,
				Label:       "Passive mode (PASV)",
				I18nKey:     "storages.fields.passive",
				Default:     true,
				Help:        "Off switches to active mode, which needs the server to dial back into this host.",
				HelpI18nKey: "storages.fieldHelp.passive",
				Advanced:    true,
			},
		},
	})
}
