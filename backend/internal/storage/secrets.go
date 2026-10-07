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
// masked credential is only kept while they stay as they were (KeepSecrets).
var addressWords = []string{"host", "hostname", "endpoint", "url", "uri", "server", "address"}

// ErrSecretAddressChanged is what KeepSecrets answers for a credential that
// came back masked while the address it would be sent to changed (or the
// driver did). Wrapped in a *SecretAddressError naming the field.
var ErrSecretAddressChanged = errors.New("storage: a saved credential is only sent where it was saved")

// SecretAddressError names the credential that has to be typed again.
type SecretAddressError struct{ Field string }

func (e *SecretAddressError) Error() string {
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
		if prevDriver != driver || addressChanged(cfg, old) {
			return nil, &SecretAddressError{Field: masked[0]}
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
