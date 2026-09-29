package perm

import (
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A custom role in other languages (migration 00071): Names and Descriptions
// on model.PermissionRule, language code -> text. The role's own Name and
// Description stay required-and-fallback; a reader sees the translation for
// their interface language (PermissionRule.NameFor, Source.RuleNameFor).

// MaxRuleDescriptionLen bounds a translated description: what the role's own
// description column holds on MySQL (00069, VARCHAR(1000)). A translated name
// is bounded like the name (MaxRuleNameLen).
const MaxRuleDescriptionLen = 1000

// normalizeRuleTexts trims r's translations, drops the blank ones, and keys
// the rest by the catalogue's form of the language. A language filex does not
// offer (srvtext.Offered: shipped, or added by a running language pack) is
// refused — unless prev already carried it: a role translated for a pack that
// has since been removed must stay savable, and its entry reads again the day
// the pack returns.
func normalizeRuleTexts(r, prev *model.PermissionRule) error {
	kept := map[string]bool{}
	if prev != nil {
		for k := range prev.Names {
			kept[k] = true
		}
		for k := range prev.Descriptions {
			kept[k] = true
		}
	}
	var err error
	if r.Names, err = normalizeTexts("name", r.Names, MaxRuleNameLen, kept); err != nil {
		return err
	}
	r.Descriptions, err = normalizeTexts("description", r.Descriptions, MaxRuleDescriptionLen, kept)
	return err
}

// normalizeTexts is normalizeRuleTexts for one map; nil when nothing is left.
func normalizeTexts(field string, in map[string]string, limit int, kept map[string]bool) (map[string]string, error) {
	var out map[string]string
	for tag, text := range in {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		code, ok := srvtext.Offered(tag)
		if !ok && (code == "" || !kept[code]) {
			return nil, invalid("%s in %q: not an interface language this server offers", field, tag)
		}
		if len([]rune(text)) > limit {
			return nil, invalid("%s in %q is longer than %d characters", field, code, limit)
		}
		if _, dup := out[code]; dup {
			return nil, invalid("%s in %q is given twice", field, code)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[code] = text
	}
	return out, nil
}
