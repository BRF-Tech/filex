package sftp

import "github.com/brf-tech/filex/backend/internal/storage"

func intp(v int) *int { return &v }

// Config contract for the sftp driver — every key Init reads, declared
// once so the admin form, the replication-target dialog, the CLI and
// storage.ValidateNonRootPath all agree. See storage/descriptor.go.
//
// The aliases are load-bearing: the admin form used to send "base_path"
// and the replication dialog "username", neither of which any Init or the
// validator ever read. Init now reads them as legacy spellings of "root"
// and "user" so rows written before this descriptor keep working.
func init() {
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  "sftp",
		Label:   "SFTP",
		I18nKey: "storages.driver.sftp",
		Fields: []storage.Field{
			{
				Key:         "host",
				Type:        storage.FieldString,
				Label:       "Host",
				I18nKey:     "storages.fields.host",
				Placeholder: "files.example.com",
				Required:    true,
				Monospace:   true,
			},
			{
				Key:     "port",
				Type:    storage.FieldInt,
				Label:   "Port",
				I18nKey: "storages.fields.port",
				Default: 22,
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
				Key:         "password",
				Type:        storage.FieldPassword,
				Label:       "Password",
				I18nKey:     "storages.fields.password",
				Secret:      true,
				Help:        "Either a password or a private key is required.",
				HelpI18nKey: "storages.fieldHelp.sftpAuth",
			},
			{
				Key:         "private_key",
				Type:        storage.FieldPassword,
				Label:       "Private key (PEM)",
				I18nKey:     "storages.fields.privateKey",
				Secret:      true,
				Multiline:   true,
				Monospace:   true,
				Placeholder: "-----BEGIN OPENSSH PRIVATE KEY-----",
			},
			{
				Key:         "key_path",
				Type:        storage.FieldString,
				Label:       "Private key file",
				I18nKey:     "storages.fields.keyPath",
				Help:        "Path to a key file on the server, read when the storage starts. Used when the PEM field is empty.",
				HelpI18nKey: "storages.fieldHelp.keyPath",
				Placeholder: "/etc/filex/keys/sftp_id_ed25519",
				Monospace:   true,
			},
			{
				Key:         "root",
				Type:        storage.FieldString,
				Label:       "Base path",
				I18nKey:     "storages.fields.root",
				Help:        "Sub-folder on the remote host. Required: filex never mounts the account root.",
				HelpI18nKey: "storages.fieldHelp.root",
				Placeholder: "/srv/files",
				Required:    true,
				Monospace:   true,
				Root:        true,
				Aliases:     []string{"base_path", "remote_path"},
			},
			{
				Key:         "known_hosts",
				Type:        storage.FieldString,
				Label:       "known_hosts file",
				I18nKey:     "storages.fields.knownHosts",
				Help:        "OpenSSH known_hosts path for strict host-key checking. Default is trust-on-first-use in ~/.filex/known_hosts.",
				HelpI18nKey: "storages.fieldHelp.knownHosts",
				Placeholder: "/etc/filex/known_hosts",
				Monospace:   true,
				Advanced:    true,
			},
			{
				Key:         "host_key",
				Type:        storage.FieldString,
				Label:       "Pinned host key",
				I18nKey:     "storages.fields.hostKey",
				Help:        "A single public key in authorized_keys / known_hosts line form.",
				HelpI18nKey: "storages.fieldHelp.hostKey",
				Monospace:   true,
				Advanced:    true,
			},
			{
				Key:         "insecure_skip_host_key",
				Type:        storage.FieldBool,
				Label:       "Skip host-key verification (insecure)",
				I18nKey:     "storages.fields.insecureSkipHostKey",
				Help:        "Accepts any host key. Only for throwaway hosts.",
				HelpI18nKey: "storages.fieldHelp.insecureSkipHostKey",
				Default:     false,
				Advanced:    true,
			},
		},
	})
}
