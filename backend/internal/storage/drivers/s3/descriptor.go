package s3

import "github.com/brf-tech/filex/backend/internal/storage"

// Config contract for the s3 driver — every key Init reads, declared once
// so the admin form, the replication-target dialog, the CLI and
// storage.ValidateNonRootPath all agree. See storage/descriptor.go.
//
// `prefix` is the field the admin form never had: without it every submit
// came back 400 ROOT_PATH_FORBIDDEN, which is the bug this file closes.
func init() {
	storage.RegisterDescriptor(storage.Descriptor{
		Driver:  "s3",
		Label:   "S3 / Hetzner / MinIO",
		I18nKey: "storages.driver.s3",
		Fields: []storage.Field{
			{
				Key:         "bucket",
				Type:        storage.FieldString,
				Label:       "Bucket",
				I18nKey:     "storages.fields.bucket",
				Placeholder: "my-bucket",
				Required:    true,
			},
			{
				Key:         "prefix",
				Type:        storage.FieldString,
				Label:       "Prefix",
				I18nKey:     "storages.fields.prefix",
				Help:        "Sub-folder inside the bucket. Required: filex never takes ownership of the bucket root.",
				HelpI18nKey: "storages.fieldHelp.prefix",
				Placeholder: "fileman",
				Required:    true,
				Monospace:   true,
				Root:        true,
			},
			{
				Key:         "region",
				Type:        storage.FieldString,
				Label:       "Region",
				I18nKey:     "storages.fields.region",
				Help:        "Defaults to \"auto\" when left empty.",
				HelpI18nKey: "storages.fieldHelp.region",
				Placeholder: "eu-central",
			},
			{
				Key:     "endpoint",
				Type:    storage.FieldString,
				Label:   "Endpoint",
				I18nKey: "storages.fields.endpoint",
				// Not required: empty means AWS S3 proper. Any other
				// S3-compatible store needs its endpoint here.
				Help:        "Leave empty for AWS S3. Any S3-compatible store needs its endpoint.",
				HelpI18nKey: "storages.fieldHelp.endpoint",
				Placeholder: "https://nbg1.your-objectstorage.com",
				Monospace:   true,
			},
			{
				Key:     "access_key",
				Type:    storage.FieldPassword,
				Label:   "Access key",
				I18nKey: "storages.fields.accessKey",
				Secret:  true,
				// Not Required: an install running on an instance role
				// leaves both credential fields empty on purpose (Init
				// then lets the AWS SDK resolve the chain).
				Monospace: true,
			},
			{
				Key:       "secret_key",
				Type:      storage.FieldPassword,
				Label:     "Secret key",
				I18nKey:   "storages.fields.secretKey",
				Secret:    true,
				Monospace: true,
			},
			{
				Key:         "path_style",
				Type:        storage.FieldBool,
				Label:       "Use path-style URLs (Hetzner, MinIO)",
				I18nKey:     "storages.fields.pathStyle",
				Default:     true,
				Help:        "On for every non-AWS store. Init turns it on by itself when an endpoint is set and this was never touched.",
				HelpI18nKey: "storages.fieldHelp.pathStyle",
			},
			{
				Key:         "disable_presign",
				Type:        storage.FieldBool,
				Label:       "Disable presigned URLs",
				I18nKey:     "storages.fields.disablePresign",
				Help:        "Turn on when the store rejects SDK-signed URLs (Ceph RGW / some Hetzner setups answer SignatureDoesNotMatch); uploads then stream through the backend.",
				HelpI18nKey: "storages.fieldHelp.disablePresign",
				Default:     false,
				Advanced:    true,
			},
		},
	})
}
