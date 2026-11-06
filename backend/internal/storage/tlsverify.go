package storage

// Whether a driver that speaks TLS checks the certificate of the server it
// connects to (issue #134).
//
// A private S3 endpoint, WebDAV or FTPS server often answers with a certificate
// no public authority signed, and every connection to it failed. The way that
// keeps the check is to trust that certificate: see "Private and self-signed
// certificates" in docs/STORAGE.md. This switch is for the server where that is
// not an option.
//
// It is the connection's own setting (`insecure_skip_tls_verify`), not the
// instance's: 0.58.2 had one variable for every storage, and the owner's call
// was that one self-signed NAS must not take the check away from the S3 bucket
// beside it. A replication target is a connection of its own, with its own
// switch. The variable is a one-time seed since (server/storage_tls_seed.go).
//
// ⚠⚠ With it on, anybody who can sit between filex and the server can read and
// change every file that crosses, and the storage's credentials on the drivers
// that send them (WebDAV, FTPS). It is off unless an operator turns it on.

import "crypto/tls"

// SkipTLSVerifyKey is the config key of the switch. A driver reads it as a
// literal (`cfg["insecure_skip_tls_verify"]`) so TestDescriptorMatchesInit sees
// the read; this name is for everything that is not a driver.
const SkipTLSVerifyKey = "insecure_skip_tls_verify"

// SkipTLSVerifyField is the switch as a driver's descriptor declares it: one
// label, one help and one warning for every driver that has it. A fresh value,
// so a driver can set Requires on its own copy.
func SkipTLSVerifyField() Field {
	return Field{
		Key:         SkipTLSVerifyKey,
		Type:        FieldBool,
		Label:       "Skip certificate verification (insecure)",
		I18nKey:     "storages.fields.insecureSkipTlsVerify",
		Help:        "Accepts any certificate from this server: for a self-signed or private certificate that cannot be trusted any other way.",
		HelpI18nKey: "storages.fieldHelp.insecureSkipTlsVerify",
		Default:     false,
		Advanced:    true,
		SecurityOff: &SecurityOff{
			Risk: "Anybody who can get between filex and this server can pose as it: read and change every file that crosses, " +
				"and take this connection's credentials. Trusting the certificate keeps the check on: see Storage in the documentation.",
			RiskI18nKey: "storages.fieldRisk.insecureSkipTlsVerify",
		},
	}
}

// ApplyTLSVerify is what a driver passes its TLS configuration through, with
// the connection's own answer. With the check on, base comes back as it went
// in, nil included, so a driver's connection stays exactly what it was. With it
// off, it comes back as a copy that accepts any certificate: base may be shared
// and is never changed.
func ApplyTLSVerify(base *tls.Config, skip bool) *tls.Config {
	if !skip {
		return base
	}
	out := base.Clone()
	if out == nil {
		out = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	out.InsecureSkipVerify = true //nolint:gosec // the operator's explicit choice, see the file comment
	return out
}
