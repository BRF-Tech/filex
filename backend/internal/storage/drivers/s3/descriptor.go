package s3

import (
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

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
		Fields: append([]storage.Field{
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
				Label:       "Stream transfers through filex",
				I18nKey:     "storages.fields.disablePresign",
				Help:        "On by default: uploads and the downloads behind share links stream through filex, so the bucket endpoint never has to be reachable from a browser. Turn off only when it is (AWS, a public MinIO) and you want the browser to talk to the bucket directly.",
				HelpI18nKey: "storages.fieldHelp.disablePresign",
				Default:     true,
				Advanced:    true,
			},
		}, defaults.Fields(timeoutTexts)...),
	})
}

// timeoutTexts are the S3 form's texts for the three settings of issue #44
// (resilience.go). With the defaults a dead store is reported within 15 s;
// before them a drop into one took 85 s, and a store that accepted the
// connection but never answered did not return at all. S3 names its own
// store-side waits, so the attempt timeout and the attempts have their own
// catalogue entries; the other network drivers share stall.ServerTexts.
var timeoutTexts = stall.Texts{
	AttemptTimeout: stall.FieldText{
		Label:       "Attempt timeout (seconds)",
		I18nKey:     "storages.fields.s3AttemptTimeout",
		Help:        "How long one attempt may wait for a sign of life from the store: connecting, the TLS handshake, the answer after the request is sent, and each next piece of the answer. A transfer that keeps moving is never cut, however long it takes; an upload the store stops taking is cut after 60 seconds, or after this if it is longer. Copies and renames wait up to 10 minutes for the answer, because the store answers them only when the copy is done.",
		HelpI18nKey: "storages.fieldHelp.s3AttemptTimeout",
	},
	MaxAttempts: stall.FieldText{
		Label:       "Attempts per request",
		I18nKey:     "storages.fields.s3MaxAttempts",
		Help:        "How many times a request is tried in all when the failure can pass: a network error, a timeout, a 5xx answer, throttling. 1 turns retrying off. A refusal (403, a missing bucket, a host name that does not resolve) is never retried.",
		HelpI18nKey: "storages.fieldHelp.s3MaxAttempts",
	},
	TotalTimeout: stall.FieldText{
		Label:       "Give up after (seconds)",
		I18nKey:     "storages.fields.s3TotalTimeout",
		Help:        "No new attempt starts unless it could finish within this many seconds of the first, so an upload or a listing on a store that is down reports the outage within this time. A transfer that is under way is not cut. A request that timed out is tried again only when this is at least twice the attempt timeout.",
		HelpI18nKey: "storages.fieldHelp.s3TotalTimeout",
	},
}
