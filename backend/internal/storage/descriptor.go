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

	// LazyFields are the lazy catalogue's settings (LazyFields()), offered
	// only for a driver a storage may be catalogued lazily on
	// (model.LazyDrivers). Same map, same renderer as ScanFields; filled by
	// the admin descriptor endpoint, which knows which drivers those are.
	LazyFields []Field `json:"lazy_fields,omitempty"`
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

// Lazy catalogue settings (sync_mode `lazy`, issue #45 — docs/LAZY-CATALOGUE.md).
// They live in the storage's config map beside ScanExcludeKey, and like it they
// are the scan's, not the driver's: Init never reads them.
const (
	// LazyFillKey chooses the behaviour: LazyFillBackground (A, the default)
	// or LazyFillOnOpen (B).
	LazyFillKey = "lazy_fill"
	// LazyMaxWatchesKey caps the fsnotify watches on visited folders.
	LazyMaxWatchesKey = "lazy_max_watches"
	// LazyWatchTTLKey is how many minutes a folder nobody opens keeps its watch.
	LazyWatchTTLKey = "lazy_watch_ttl"

	LazyFillBackground = "background"
	LazyFillOnOpen     = "on_open"

	LazyMaxWatchesDefault = 1024
	LazyWatchTTLDefault   = 60 // minutes
)

// LazyFields returns the lazy catalogue's settings for a storage on a driver
// that supports it (model.LazyDrivers), in the shape every other storage
// field has, so the storage form draws them with the same renderer. A fresh
// slice: callers cannot edit the set.
func LazyFields() []Field {
	minOne := 1
	maxWatches := 1_000_000
	maxTTL := 7 * 24 * 60
	return []Field{
		{
			Key:     LazyFillKey,
			Type:    FieldSelect,
			Label:   "Catalog behavior",
			I18nKey: "storages.fields.lazyFill",
			Help: "Click first, fill in the background: the folder somebody opens is listed from disk at once and cataloged " +
				"first, and a throttled background pass catalogs the rest, so search, folder sizes and usage end up covering " +
				"everything. Only on open: nothing runs in the background — only the folders people visit are cataloged, and " +
				"search, folder sizes and usage say they cover those folders only.",
			HelpI18nKey: "storages.fieldHelp.lazyFill",
			Default:     LazyFillBackground,
			Options: []SelectOption{
				{Value: LazyFillBackground, Label: "Click first, fill in the background", I18nKey: "storages.lazyFill.background"},
				{Value: LazyFillOnOpen, Label: "Only on open", I18nKey: "storages.lazyFill.on_open"},
			},
		},
		{
			Key:         LazyMaxWatchesKey,
			Type:        FieldInt,
			Label:       "Watched folders (at most)",
			I18nKey:     "storages.fields.lazyMaxWatches",
			Help:        "Visited folders are watched for changes made outside filex. Past this many, the folder opened longest ago stops being watched and is checked again the next time somebody opens it.",
			HelpI18nKey: "storages.fieldHelp.lazyMaxWatches",
			Default:     LazyMaxWatchesDefault,
			Min:         &minOne,
			Max:         &maxWatches,
			Advanced:    true,
		},
		{
			Key:         LazyWatchTTLKey,
			Type:        FieldInt,
			Label:       "Stop watching after (minutes unopened)",
			I18nKey:     "storages.fields.lazyWatchTTL",
			Help:        "A folder nobody has opened for this long stops being watched; it is checked again the next time somebody opens it.",
			HelpI18nKey: "storages.fieldHelp.lazyWatchTTL",
			Default:     LazyWatchTTLDefault,
			Min:         &minOne,
			Max:         &maxTTL,
			Advanced:    true,
		},
	}
}

// ValidateLazyConfig checks the lazy catalogue's settings in a storage's
// config map against LazyFields(): the behaviour must be one of its options
// and the two numbers within their bounds. A key that is absent (or empty)
// is fine — the default applies. The admin API refuses a storage that fails.
func ValidateLazyConfig(cfg map[string]any) error {
	for _, f := range LazyFields() {
		v, ok := ConfigLookup(cfg, f.Key)
		if !ok || v == nil {
			continue
		}
		switch f.Type {
		case FieldSelect:
			s, isStr := v.(string)
			if !isStr {
				return fmt.Errorf("%s must be one of %s", f.Key, optionValues(f.Options))
			}
			if s == "" {
				continue
			}
			found := false
			for _, o := range f.Options {
				found = found || o.Value == s
			}
			if !found {
				return fmt.Errorf("%s must be one of %s", f.Key, optionValues(f.Options))
			}
		case FieldInt:
			n, isNum := lazyInt(v)
			if !isNum {
				if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
					continue
				}
				return fmt.Errorf("%s must be a whole number", f.Key)
			}
			if (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
				return fmt.Errorf("%s must be between %d and %d", f.Key, *f.Min, *f.Max)
			}
		}
	}
	return nil
}

func optionValues(opts []SelectOption) string {
	vals := make([]string, 0, len(opts))
	for _, o := range opts {
		vals = append(vals, o.Value)
	}
	return strings.Join(vals, ", ")
}

// lazyInt reads a number the way JSON and a form deliver it.
func lazyInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &n); err != nil || fmt.Sprint(n) != strings.TrimSpace(x) {
			return 0, false
		}
		return n, true
	}
	return 0, false
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
