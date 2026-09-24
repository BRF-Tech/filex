package local

import "github.com/brf-tech/filex/backend/internal/storage"

// Config contract for the local driver — every key Init reads, declared
// once so the admin form, the replication-target dialog, the CLI and
// storage.ValidateNonRootPath all agree. See storage/descriptor.go.
func init() {
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  "local",
		Label:   "Local filesystem",
		I18nKey: "storages.driver.local",
		Fields: []storage.Field{
			{
				Key:         "path",
				Type:        storage.FieldString,
				Label:       "Filesystem path",
				I18nKey:     "storages.fields.path",
				Help:        "Directory on the server. It is created if missing; filex never mounts /.",
				HelpI18nKey: "storages.fieldHelp.path",
				Placeholder: "/var/lib/filex/data",
				Required:    true,
				Monospace:   true,
				Root:        true,
				// "root" is the pre-0.19 spelling; Init still reads it.
				Aliases: []string{"root"},
			},
			{
				Key:     "follow_symlinks",
				Type:    storage.FieldBool,
				Label:   "Follow symlinks that leave this folder",
				I18nKey: "storages.fields.followSymlinks",
				Default: false,
				Help: "A symlink pointing INSIDE this folder is always followed. " +
					"This governs only links pointing outside it: off, they are listed " +
					"but cannot be opened, written to or deleted through; on, filex " +
					"treats the linked files as part of the storage — including for " +
					"indexing, virus scanning, quota and deletion.",
				HelpI18nKey: "storages.fieldHelp.followSymlinks",
				Advanced:    true,
			},
		},
	})
}
