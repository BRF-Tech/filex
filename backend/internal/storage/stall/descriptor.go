package stall

import "github.com/brf-tech/filex/backend/internal/storage"

// FieldText is one setting's label and help, in English, with the catalogue
// keys the surfaces translate them by.
//
// ⚠ The key fields are spelled I18nKey / HelpI18nKey, like storage.Field's,
// and this file is named descriptor.go, on purpose: web/tests/i18n/
// storageDescriptorKeys.test.ts finds every key by that spelling in every
// descriptor.go under internal/storage and fails when the admin catalogue has
// no English or Turkish for it. Another spelling, or another file name, and
// a key missing from the catalogue ships as English on a Turkish screen.
type FieldText struct {
	Label, I18nKey    string
	Help, HelpI18nKey string
}

// Texts are the three settings' texts.
type Texts struct {
	AttemptTimeout, MaxAttempts, TotalTimeout FieldText
}

// ServerTexts are the texts every driver but S3 shows (S3's name its own
// store-side waits, see drivers/s3/descriptor.go). attempt is the attempt
// timeout's: AttemptText, or a driver's own when some of its requests wait
// longer for the server's answer (WebDAV's copies and moves).
func ServerTexts(attempt FieldText) Texts {
	return Texts{
		AttemptTimeout: attempt,
		MaxAttempts: FieldText{
			Label:       "Attempts per request",
			I18nKey:     "storages.fields.maxAttempts",
			Help:        "How many times a request is tried in all when the failure can pass: the connection is refused or drops, no answer comes in time, the server answers that it is unavailable (5xx). 1 turns retrying off. A refusal (a wrong password, a missing folder, a certificate that does not verify) is never retried, and neither is an upload that has started sending.",
			HelpI18nKey: "storages.fieldHelp.maxAttempts",
		},
		TotalTimeout: FieldText{
			Label:       "Give up after (seconds)",
			I18nKey:     "storages.fields.totalTimeout",
			Help:        "No new attempt starts unless it could finish within this many seconds of the first, so a server that is down is reported within this time. A transfer that is under way is not cut. A request that timed out is tried again only when this is at least twice the attempt timeout.",
			HelpI18nKey: "storages.fieldHelp.totalTimeout",
		},
	}
}

// AttemptText is the attempt-timeout text of ServerTexts for a driver whose
// server answers every request at once (FTP, SFTP, SMB).
var AttemptText = FieldText{
	Label:       "Attempt timeout (seconds)",
	I18nKey:     "storages.fields.attemptTimeout",
	Help:        "How long one attempt may wait for a sign of life from the server: connecting, logging in, the answer to each request, and each next piece of a transfer. A transfer that keeps moving is never cut, however long it takes; an upload the server stops taking is cut after 60 seconds, or after this if it is longer.",
	HelpI18nKey: "storages.fieldHelp.attemptTimeout",
}

// Fields are the three settings as descriptor fields, under Advanced, with
// the driver's defaults and the shared bounds.
func (d Defaults) Fields(t Texts) []storage.Field {
	field := func(key string, text FieldText, def, hi int) storage.Field {
		return storage.Field{
			Key:         key,
			Type:        storage.FieldInt,
			Label:       text.Label,
			I18nKey:     text.I18nKey,
			Help:        text.Help,
			HelpI18nKey: text.HelpI18nKey,
			Default:     def,
			Min:         intp(1),
			Max:         intp(hi),
			Advanced:    true,
		}
	}
	return []storage.Field{
		field("attempt_timeout_s", t.AttemptTimeout, d.AttemptTimeoutS, AttemptTimeoutLimitS),
		field("max_attempts", t.MaxAttempts, d.MaxAttempts, MaxAttemptsLimit),
		field("total_timeout_s", t.TotalTimeout, d.TotalTimeoutS, TotalTimeoutLimitS),
	}
}

func intp(v int) *int { return &v }
