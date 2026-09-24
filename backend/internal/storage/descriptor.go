package storage

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────
// Driver config descriptors.
//
// Before descriptors the registry exposed driver *names* only, so every
// surface that had to collect a driver config invented its own form:
// the admin "new storage" page, the replication-target dialog, the CLI's
// --config help text and the root-path validator each carried a private,
// hand-maintained list of keys. They drifted, and three of the four
// drivers the admin UI offered could not be created through it at all —
// the form never collected the key ValidateNonRootPath reads, so every
// submit came back 400 ROOT_PATH_FORBIDDEN.
//
// A Descriptor is the single machine-readable answer to "what does this
// driver need?". Drivers declare it next to their Init so the two cannot
// drift (a test parses Init and fails when a config key is read but not
// declared, or declared but never read); every surface renders from it.
// ─────────────────────────────────────────────────────────────────────

// FieldType tells a surface which widget to render for a config field.
type FieldType string

// Field types. Keep this list small — a surface that meets an unknown
// type should fall back to a plain text input rather than hide the field.
const (
	FieldString   FieldType = "string"
	FieldInt      FieldType = "int"
	FieldBool     FieldType = "bool"
	FieldPassword FieldType = "password"
	FieldSelect   FieldType = "select"
)

// SelectOption is one choice of a FieldSelect field.
type SelectOption struct {
	Value string `json:"value"`
	// Label is the English fallback shown when I18nKey is missing from
	// the surface's catalogue.
	Label   string `json:"label"`
	I18nKey string `json:"i18n_key,omitempty"`
}

// Field describes exactly one key of a driver's config map.
//
// Key is the wire name — what lands in storages.config_json and what the
// driver's Init reads. Aliases are historical spellings that Init still
// honours so rows written before this descriptor existed keep working.
type Field struct {
	Key  string    `json:"key"`
	Type FieldType `json:"type"`

	// Label / Help are the English fallbacks. Surfaces resolve I18nKey /
	// HelpI18nKey first and only fall back to these — never render a
	// hardcoded English string when a translation exists.
	Label       string `json:"label"`
	Help        string `json:"help,omitempty"`
	I18nKey     string `json:"i18n_key"`
	HelpI18nKey string `json:"help_i18n_key,omitempty"`

	Required bool `json:"required"`
	// Secret marks credential material: surfaces must not echo it in
	// logs/URLs and should render a masked input.
	Secret bool `json:"secret"`

	Default     any            `json:"default,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`
	Min         *int           `json:"min,omitempty"`
	Max         *int           `json:"max,omitempty"`

	// Monospace hints that the value is a path / URL / key blob.
	Monospace bool `json:"monospace,omitempty"`
	// Multiline hints at a textarea (PEM blobs).
	Multiline bool `json:"multiline,omitempty"`
	// Advanced fields are collapsed by default — rarely needed knobs.
	Advanced bool `json:"advanced,omitempty"`

	// Root marks THE field that scopes the storage inside the backend
	// (s3 prefix, local path, sftp/ftp/webdav root). ValidateNonRootPath
	// reads this field instead of its own hardcoded per-driver list.
	Root bool `json:"root,omitempty"`

	// Aliases are legacy keys Init still reads for this field.
	Aliases []string `json:"aliases,omitempty"`
}

// Descriptor is a driver's complete, machine-readable config contract.
type Descriptor struct {
	Driver string `json:"driver"`
	// Label / I18nKey name the driver in a picker.
	Label   string  `json:"label"`
	I18nKey string  `json:"i18n_key"`
	Fields  []Field `json:"fields"`

	// Capabilities is the driver's runtime feature set (ComputeCapabilities
	// on a fresh instance). Surfaces want it in the same payload: a picker
	// can say "no presigned URLs" before anything is saved.
	Capabilities Capabilities `json:"capabilities"`

	// ScanFields are the settings a STORAGE on this driver has for the scan
	// that catalogues it (ScanFields()), as opposed to Fields, which the
	// driver's Init reads. They live in the same config map and are drawn by
	// the same form component, but only where a storage is edited: a
	// replication target is never scanned, so its dialog leaves them out.
	// Filled by Descriptors(); the registry does not hold them, which keeps
	// TestDescriptorMatchesInit about what Init reads.
	ScanFields []Field `json:"scan_fields,omitempty"`
}

// ScanExcludeKey is the storage config key holding a storage's scan
// exclusions: glob patterns, one per line, for paths the scan must not walk.
// internal/scanrule reads it; the key lives here so the descriptor that offers
// it and the code that reads it name the same thing.
const ScanExcludeKey = "scan_exclude"

// ScanFields returns the scan settings every storage has, whatever its driver.
// A fresh slice: callers cannot edit the set.
//
// ⚠ The help text says what the setting is NOT, on purpose: an excluded path is
// not walked, catalogued, indexed, thumbnailed or virus-scanned, but it is still
// on the storage and still served to whoever asks for it by path — the file
// protocols and the AI tools read the storage directly. It saves work; it is
// not an access control.
func ScanFields() []Field {
	return []Field{{
		Key:     ScanExcludeKey,
		Type:    FieldString,
		Label:   "Paths to exclude from scanning",
		I18nKey: "storages.fields.scanExclude",
		Help: "One pattern per line, relative to the storage root: * matches within a name, " +
			"** any number of folders, and a pattern without a / matches that name at any depth " +
			"(.* skips every hidden file and folder). The scan does not go into a matching folder, " +
			"and matching files are not catalogued, indexed, thumbnailed or virus-scanned. This " +
			"saves work; it is not access control — the files stay on the storage, reachable by " +
			"path, over WebDAV/SFTP and through the AI tools. Anything catalogued before you add " +
			"a pattern stays as it is.",
		HelpI18nKey: "storages.fieldHelp.scanExclude",
		Placeholder: ".*\ndownloads/incomplete/**\n*.tmp",
		Monospace:   true,
		Multiline:   true,
	}}
}

// Field returns the field with the given key.
func (d Descriptor) Field(key string) (Field, bool) {
	for _, f := range d.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// RootField returns the field flagged Root, if the driver has one.
func (d Descriptor) RootField() (Field, bool) {
	for _, f := range d.Fields {
		if f.Root {
			return f, true
		}
	}
	return Field{}, false
}

// Keys returns every declared key plus its aliases — the complete set of
// config keys this driver understands.
func (d Descriptor) Keys() []string {
	out := make([]string, 0, len(d.Fields))
	for _, f := range d.Fields {
		out = append(out, f.Key)
		out = append(out, f.Aliases...)
	}
	sort.Strings(out)
	return out
}

// MissingRequired lists required fields absent (or blank) from cfg,
// honouring aliases. Fields carrying a Default are never "missing" — the
// driver fills them in.
func (d Descriptor) MissingRequired(cfg map[string]any) []string {
	var out []string
	for _, f := range d.Fields {
		if !f.Required || f.Default != nil {
			continue
		}
		if _, ok := ConfigLookup(cfg, f.Key, f.Aliases...); !ok {
			out = append(out, f.Key)
		}
	}
	return out
}

// ConfigLookup returns the first non-empty value for key or any alias.
// Empty strings count as absent — a blank text input is not a value.
func ConfigLookup(cfg map[string]any, key string, aliases ...string) (any, bool) {
	for _, k := range append([]string{key}, aliases...) {
		v, ok := cfg[k]
		if !ok || v == nil {
			continue
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
			continue
		}
		return v, true
	}
	return nil, false
}

// ConfigString is ConfigLookup narrowed to strings.
func ConfigString(cfg map[string]any, key string, aliases ...string) string {
	v, ok := ConfigLookup(cfg, key, aliases...)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

var (
	descMu      sync.RWMutex
	descriptors = map[string]Descriptor{}
)

// RegisterDescriptor records a driver's config contract. Called from the
// driver package's init(), next to storage.Register.
func RegisterDescriptor(d Descriptor) {
	if d.Driver == "" {
		panic("storage: descriptor with empty driver name")
	}
	seen := map[string]string{}
	for _, f := range d.Fields {
		if f.Key == "" {
			panic("storage: " + d.Driver + ": descriptor field with empty key")
		}
		for _, k := range append([]string{f.Key}, f.Aliases...) {
			if prev, dup := seen[k]; dup {
				panic(fmt.Sprintf("storage: %s: config key %q declared twice (%s, %s)", d.Driver, k, prev, f.Key))
			}
			seen[k] = f.Key
		}
	}
	descMu.Lock()
	defer descMu.Unlock()
	if _, dup := descriptors[d.Driver]; dup {
		panic("storage: duplicate descriptor registration: " + d.Driver)
	}
	descriptors[d.Driver] = d
}

// UnregisterDescriptor forgets a driver's config contract — the plugin
// counterpart of Unregister. Unknown names are a no-op.
func UnregisterDescriptor(name string) {
	descMu.Lock()
	defer descMu.Unlock()
	delete(descriptors, name)
}

// DescriptorFor returns the descriptor for a driver name.
func DescriptorFor(name string) (Descriptor, bool) {
	descMu.RLock()
	defer descMu.RUnlock()
	d, ok := descriptors[name]
	return d, ok
}

// Descriptors returns every registered descriptor, driver-name sorted,
// with Capabilities computed from a fresh driver instance. Descriptors
// without a registered factory (test doubles) come back with the zero
// capability set rather than being dropped.
func Descriptors() []Descriptor {
	descMu.RLock()
	out := make([]Descriptor, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, d)
	}
	descMu.RUnlock()
	for i := range out {
		if drv, err := Get(out[i].Driver); err == nil {
			out[i].Capabilities = ComputeCapabilities(drv)
		}
		out[i].ScanFields = ScanFields()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Driver < out[j].Driver })
	return out
}
