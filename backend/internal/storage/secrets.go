package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SecretMask is what an admin surface shows in place of a stored credential
// in a driver configuration - a storage's or a replication target's. A save
// that sends it back means "keep the one I was shown" (KeepSecrets).
const SecretMask = "***"

// secretWords are the config keys that hold a credential whatever the driver
// says - for a driver without a descriptor (a plugin that did not mark its
// fields) and as a second net under one that forgot to. A key is a secret when
// it IS one of these or ends in "_" + one of them ("bind_password").
var secretWords = []string{
	"password", "passwd", "passphrase", "secret", "secret_key", "secret_access_key",
	"access_key", "token", "api_key", "apikey", "private_key", "key_pem",
	"account_key", "sas_token", "credentials",
}

// addressWords are the config keys that say WHERE a credential is sent. A
// masked credential is only kept while they stay as they were (SecretMoved).
// `issuer` is an OpenID provider's: its client secret goes to the token
// endpoint the issuer names.
var addressWords = []string{"host", "hostname", "endpoint", "url", "uri", "server", "address", "issuer"}

// notWhereSecretsGo are the keys the address words catch that name no place a
// saved credential, or anything got with one, is ever sent: changing one asks
// for nothing. The one exemption list, for every caller of SecretMoved; each
// entry says why, and a key goes here only when that holds for every
// configuration the key appears in (TestNotWhereSecretsGo).
//
// Classified one by one on 2026-11-06 (the owner: "düzeltelim hatalı
// kalmasın"), over every identity provider, SMTP and tenant-row key the words
// catch. An OpenID client secret goes to the token endpoint the issuer's
// discovery document names (auth/drivers/oidc: the code exchange, and the
// Test's client-credentials check); the access token it buys goes to the
// userinfo endpoint the same document names; jwks, authorize and end-session
// come from it too and carry no secret. So `issuer` is an address; these are
// not:
var notWhereSecretsGo = map[string]string{
	"redirect_url": "OpenID Connect: where the identity provider sends the browser back with the sign-in code. " +
		"The code is not got with the client secret, and neither the secret nor the tokens it buys are sent here.",
	"oidc_redirect_url": "The same, on a tenant's provider row (auth/drivers/multioidc).",
}

// ErrSecretAddressChanged is what KeepSecrets answers for a credential that
// came back masked while the address it would be sent to changed (or the
// driver did). Wrapped in a *SecretAddressError naming the field.
var ErrSecretAddressChanged = errors.New("storage: a saved credential is only sent where it was saved")

// SecretAddressError names the credential that has to be typed again.
type SecretAddressError struct {
	Field string
	// CheckOff: the address stands, and this save weakens a protection of the
	// connection (protections): Protection names the setting.
	CheckOff   bool
	Protection string
}

func (e *SecretAddressError) Error() string {
	switch {
	case e.CheckOff && (e.Protection == "" || e.Protection == SkipTLSVerifyKey):
		return fmt.Sprintf("the certificate check is being turned off: type %s again (a saved credential does not go to a server that is no longer checked)", e.Field)
	case e.CheckOff:
		return fmt.Sprintf("the connection's protection is being weakened (%s): type %s again (a saved credential does not go over a connection less protected than it was saved for, or one that trusts another server)", e.Protection, e.Field)
	}
	return fmt.Sprintf("the address changed: type %s again (a saved credential is only sent where it was saved)", e.Field)
}

// Is makes errors.Is(err, ErrSecretAddressChanged) true.
func (e *SecretAddressError) Is(target error) bool { return target == ErrSecretAddressChanged }

// IsSecretField reports whether key holds a credential in driver's
// configuration: a field (or an alias of one) the driver's descriptor marks
// Secret, or a key that is a credential by name (secretWords).
func IsSecretField(driver, key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	if d, ok := DescriptorFor(driver); ok {
		for _, f := range d.Fields {
			if !f.Secret {
				continue
			}
			if strings.EqualFold(f.Key, k) {
				return true
			}
			for _, a := range f.Aliases {
				if strings.EqualFold(a, k) {
					return true
				}
			}
		}
	}
	for _, w := range secretWords {
		if k == w || strings.HasSuffix(k, "_"+w) {
			return true
		}
	}
	return false
}

// MaskSecrets returns a driver configuration with every credential that is
// set replaced by SecretMask, for an admin surface to show. An empty value
// stays empty (it says "none set"); a configuration that is not a JSON object
// is returned as it is.
//
// ⚠ Every read of a configuration an administrator's browser, an API key or
// an MCP client receives goes through this - a storage's
// (/api/admin/storages) and a replication target's: an S3 secret key, an SMB
// or SFTP password, a private key. They are written, never read back.
func MaskSecrets(driver string, raw json.RawMessage) json.RawMessage {
	cfg, ok := decodeConfig(raw)
	if !ok {
		return raw
	}
	changed := false
	for k, v := range cfg {
		if !IsSecretField(driver, k) || isEmptyValue(v) {
			continue
		}
		cfg[k] = SecretMask
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return raw
	}
	return out
}

// KeepSecrets merges a configuration an admin surface sent back with the one
// stored (prev, on prevDriver): a credential that comes back as SecretMask is
// the stored one - the surface never had it to send.
//
//   - With nothing stored (a create; prev nil) a mask is dropped rather than
//     saved as three asterisks.
//   - ⚠ A mask is only kept while the credential would go where it was saved:
//     the same driver and the same address (host, endpoint, url, …). Otherwise
//     the answer is a *SecretAddressError naming the field to type again.
//     Without this, anybody who may edit a storage but not read its password -
//     an admin API key, an MCP agent - could point it at a server of their
//     own and have filex send the password there.
//   - ⚠ Nor while this save turns the connection's certificate check off (the
//     owner's call, 2026-11-06). It is the same threat by another door: with the
//     check off, whoever can answer in that server's place gets the password,
//     and the address never changed. Turning the check back on, and editing
//     anything else while it is off, asks for nothing. The 0.58.2 variable's
//     move to the connections does not come through here
//     (server/storage_tls_seed.go): nobody is editing anything then.
func KeepSecrets(driver string, next json.RawMessage, prevDriver string, prev json.RawMessage) (json.RawMessage, error) {
	cfg, ok := decodeConfig(next)
	if !ok {
		return next, nil
	}
	var masked []string
	for k, v := range cfg {
		if s, isString := v.(string); isString && s == SecretMask && IsSecretField(driver, k) {
			masked = append(masked, k)
		}
	}
	if len(masked) == 0 {
		return next, nil
	}
	sort.Strings(masked)
	old, hadPrev := decodeConfig(prev)
	if hadPrev {
		if prevDriver != driver {
			return nil, &SecretAddressError{Field: masked[0]}
		}
		if err := SecretMoved(masked, cfg, old); err != nil {
			return nil, err
		}
	}
	for _, k := range masked {
		if was, had := old[k]; hadPrev && had && !isMasked(was) {
			cfg[k] = was
		} else {
			delete(cfg, k)
		}
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return next, err
	}
	return out, nil
}

// SecretMoved is the rule every surface that keeps a saved credential follows:
// the storages and replication targets (KeepSecrets), the SMTP settings, the
// identity providers and a tenant's OIDC. A credential the save keeps from
// what is stored, rather than one typed in it, goes only to the address it was
// saved for, and over a connection at least as protected as when it was saved.
// kept names those credentials (the first, in order, is the one the answer
// names); next and prev are the configuration as it will be and as it is. A
// change of an address key (addressWords, less notWhereSecretsGo), or a save
// that weakens a protection (protections), answers a *SecretAddressError, and
// the caller writes nothing.
//
// ⚠ Measured on the real server before the rule (2026-11-06): with the address
// changed and the secret left blank, masked or not sent, the SMTP test, an
// identity provider's Test and its save, and a sign-in through a tenant's OIDC
// all sent the saved password or client secret to the new address.
func SecretMoved(kept []string, next, prev map[string]any) error {
	if len(kept) == 0 {
		return nil
	}
	names := append([]string(nil), kept...)
	sort.Strings(names)
	if addressChanged(next, prev) {
		return &SecretAddressError{Field: names[0]}
	}
	if key := protectionOff(next, prev); key != "" {
		return &SecretAddressError{Field: names[0], CheckOff: true, Protection: key}
	}
	return nil
}

// protection is a setting that keeps a connection's credential from whoever
// sits between filex and the server, and how a save weakens it.
type protection struct {
	key      string
	weakened func(key string, next, prev map[string]any) bool
}

// protections: the owner's calls of 2026-11-06. Weakening one while a saved
// credential is kept is the same threat as a new address: the credential goes
// to a server filex can no longer tell from an impostor, or in the clear.
// Turning one back on, or keeping it as it is, asks for nothing. Only a bool
// turns a switch: that is what the drivers read.
var protections = []protection{
	// S3, WebDAV, FTPS: the certificate check (tlsverify.go).
	{SkipTLSVerifyKey, switchedOn},
	// SFTP: the host-key check.
	{"insecure_skip_host_key", switchedOn},
	// FTP: FTPS; the SMTP settings pass their encryption here as a bool.
	{"tls", switchedOff},
	// LDAP: StartTLS, where the address is a plain ldap:// one.
	{"start_tls", startTLSOff},
	// What a connection trusts: a new CA (LDAP), another pinned host key or
	// another file of host keys (SFTP) is trust in another server. Taking
	// one away counts too: SFTP then trusts on first use.
	{"ca_pem", trustChanged},
	{"ca_file", trustChanged},
	{"host_key", trustChanged},
	{"known_hosts", trustChanged},
}

// protectionOff names the protection a save weakens, or "".
func protectionOff(next, prev map[string]any) string {
	for _, p := range protections {
		if p.weakened(p.key, next, prev) {
			return p.key
		}
	}
	return ""
}

func isOn(v any) bool { b, _ := v.(bool); return b }

func switchedOn(k string, next, prev map[string]any) bool { return isOn(next[k]) && !isOn(prev[k]) }

func switchedOff(k string, next, prev map[string]any) bool { return isOn(prev[k]) && !isOn(next[k]) }

func startTLSOff(k string, next, prev map[string]any) bool {
	url, _ := next["url"].(string)
	return switchedOff(k, next, prev) && strings.HasPrefix(strings.ToLower(strings.TrimSpace(url)), "ldap://")
}

func trustChanged(k string, next, prev map[string]any) bool {
	return textOf(next[k]) != textOf(prev[k])
}

func textOf(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// addressChanged reports whether any address key differs between two
// configurations (an absent key and an empty one are the same).
func addressChanged(next, prev map[string]any) bool {
	seen := map[string]bool{}
	for _, m := range []map[string]any{next, prev} {
		for k := range m {
			lk := strings.ToLower(k)
			if seen[lk] || !isAddressKey(lk) {
				continue
			}
			seen[lk] = true
			if addressValue(next, k) != addressValue(prev, k) {
				return true
			}
		}
	}
	return false
}

func isAddressKey(k string) bool {
	if _, exempt := notWhereSecretsGo[k]; exempt {
		return false
	}
	return caughtByAddressWords(k)
}

func caughtByAddressWords(k string) bool {
	for _, w := range addressWords {
		if k == w || strings.HasSuffix(k, "_"+w) {
			return true
		}
	}
	return false
}

func addressValue(m map[string]any, key string) string {
	for k, v := range m {
		if strings.EqualFold(k, key) && v != nil {
			return strings.ToLower(strings.TrimRight(strings.TrimSpace(fmt.Sprint(v)), "/"))
		}
	}
	return ""
}

func decodeConfig(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg == nil {
		return nil, false
	}
	return cfg, true
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	}
	return false
}

func isMasked(v any) bool {
	s, ok := v.(string)
	return ok && s == SecretMask
}
