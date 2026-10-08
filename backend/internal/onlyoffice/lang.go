package onlyoffice

// The editor's language (GitHub Discussion #93, task #214).
//
// ONLYOFFICE draws its whole interface - menus, dialogs, the ribbon - in
// editorConfig.lang, and formats dates, times and currency in the spreadsheet
// (and, from Docs 8.2, picks the default measurement unit) by
// editorConfig.region. filex used to send the language the request named,
// else "en", and no screen named one, so the editor was English for everybody
// whatever language filex itself spoke to them.
//
// The server decides, here and nowhere else (Service.EditorLocale, called by
// BuildConfigForNode), so every way the editor opens - the explorer's viewer,
// the editor tab, the desktop app's document windows, the embeds, all of them
// asking /api/files/onlyoffice/config - gets the same answer:
//
//  1. the administrator's fixed language (External services → ONLYOFFICE →
//     Editor language, or FILEX_ONLYOFFICE_LANG), unless it is "auto";
//  2. the language the request names (`lang`);
//  3. the request's Accept-Language: the language on the person's screen,
//     which the viewer sends with the request (core PreviewModal);
//  4. the account's language;
//  5. the instance default (FILEX_DEFAULT_LOCALE);
//  6. English.
//
// A candidate counts only when ONLYOFFICE offers it (editorLanguages, from the
// Docs API reference); one it does not offer is passed over for the next, and
// a regional or older tag reaches its nearest one (de-AT → de, nb → no,
// zh-HK → zh-TW, pt-AO → pt-PT).

import (
	"context"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
)

// EditorLangAuto is the setting that lets each person's own language decide.
const EditorLangAuto = "auto"

// EditorLanguage is one language ONLYOFFICE's editor is offered in, as the
// administrator's list shows it: the code editorConfig.lang takes and the
// language's name in itself (the way a language list names its entries, so a
// person looking for their own language finds their own word for it).
type EditorLanguage struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// editorLanguage is a row of the table below: the language, its own name and
// the regional setting it means when nothing more precise is known ("" when
// ONLYOFFICE lists none or several for it: then it derives one from lang).
type editorLanguage struct {
	code, name, region string
}

// editorLanguages is ONLYOFFICE's list, verbatim from the Docs API reference
// (editorConfig.lang, "Supported language codes"). `pt` is Portuguese
// (Brazil), `pt-PT` Portuguese (Portugal); `zh` Simplified and `zh-TW`
// Traditional Chinese; `sr` Serbian in Latin script and `sr-Cyrl` in Cyrillic.
var editorLanguages = []editorLanguage{
	{"ar", "العربية", ""},
	{"az", "Azərbaycanca", "az-Latn-AZ"},
	{"be", "Беларуская", ""},
	{"bg", "Български", "bg-BG"},
	{"ca", "Català", ""},
	{"cs", "Čeština", "cs-CZ"},
	{"da", "Dansk", "da-DK"},
	{"de", "Deutsch", "de-DE"},
	{"el", "Ελληνικά", "el-GR"},
	{"en", "English", "en-US"},
	{"es", "Español", "es-ES"},
	{"eu", "Euskara", ""},
	{"fi", "Suomi", "fi-FI"},
	{"fr", "Français", "fr-FR"},
	{"gl", "Galego", ""},
	{"he", "עברית", ""},
	{"hr", "Hrvatski", ""},
	{"hu", "Magyar", "hu-HU"},
	{"hy", "Հայերեն", ""},
	{"id", "Bahasa Indonesia", "id-ID"},
	{"it", "Italiano", "it-IT"},
	{"ja", "日本語", "ja-JP"},
	{"ko", "한국어", "ko-KR"},
	{"lo", "ລາວ", ""},
	{"lv", "Latviešu", "lv-LV"},
	{"ms", "Bahasa Melayu", ""},
	{"nl", "Nederlands", "nl-NL"},
	{"no", "Norsk", ""},
	{"pl", "Polski", "pl-PL"},
	{"pt", "Português (Brasil)", "pt-BR"},
	{"pt-PT", "Português (Portugal)", "pt-PT"},
	{"ro", "Română", ""},
	{"ru", "Русский", "ru-RU"},
	{"si", "සිංහල", ""},
	{"sk", "Slovenčina", "sk-SK"},
	{"sl", "Slovenščina", "sl-SI"},
	{"sq", "Shqip", ""},
	{"sr", "Srpski (latinica)", "sr-Latn-RS"},
	{"sr-Cyrl", "Српски (ћирилица)", "sr-Cyrl-RS"},
	{"sv", "Svenska", "sv-SE"},
	{"tr", "Türkçe", "tr-TR"},
	{"uk", "Українська", "uk-UA"},
	{"ur", "اردو", ""},
	{"vi", "Tiếng Việt", "vi-VN"},
	{"zh", "中文（简体）", "zh-CN"},
	{"zh-TW", "中文（繁體）", "zh-TW"},
}

// editorRegions is ONLYOFFICE's list of regional settings, verbatim from the
// same reference (editorConfig.region, "Supported regional settings"). A tag
// that names one of them (en-GB, de-CH, es-MX) gets it; any other region falls
// back to the language's own row above.
var editorRegions = []string{
	"ar-EG", "ar-SA", "az-Latn-AZ", "bg-BG", "cs-CZ", "da-DK", "de-AT", "de-CH", "de-DE",
	"el-GR", "en-AU", "en-GB", "en-ID", "en-US", "es-ES", "es-MX", "fi-FI", "fr-CH", "fr-FR",
	"hu-HU", "id-ID", "it-CH", "it-IT", "ja-JP", "ko-KR", "lv-LV", "nl-NL", "pl-PL", "pt-BR",
	"pt-PT", "ru-RU", "sk-SK", "sl-SI", "sr-Cyrl-RS", "sr-Latn-RS", "sv-FI", "sv-SE", "tr-TR",
	"uk-UA", "vi-VN", "zh-CN", "zh-TW",
}

var (
	langByLower   = map[string]editorLanguage{}
	regionByLower = map[string]string{}
)

func init() {
	for _, l := range editorLanguages {
		langByLower[strings.ToLower(l.code)] = l
	}
	for _, r := range editorRegions {
		regionByLower[strings.ToLower(r)] = r
	}
}

// EditorLanguages is the list the administrator picks a fixed language from,
// in ONLYOFFICE's order.
func EditorLanguages() []EditorLanguage {
	out := make([]EditorLanguage, 0, len(editorLanguages))
	for _, l := range editorLanguages {
		out = append(out, EditorLanguage{Code: l.code, Name: l.name})
	}
	return out
}

// EditorLocale is what an editor is opened in.
type EditorLocale struct {
	// Lang is editorConfig.lang, always one ONLYOFFICE offers.
	Lang string
	// Region is editorConfig.region; "" leaves it to ONLYOFFICE, which then
	// derives it from Lang.
	Region string
}

// NormalizeEditorLang reads an administrator's setting: "auto" (also "" and
// any case of it), or a language ONLYOFFICE offers, written the way its
// list writes it ("DE", "de-DE" and "de_DE" are all "de"; "pt_pt" is
// "pt-PT"). ok is false for anything else - a language ONLYOFFICE does not
// offer, or not a language tag at all.
func NormalizeEditorLang(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, EditorLangAuto) {
		return EditorLangAuto, true
	}
	loc, ok := matchEditorLang(v)
	if !ok {
		return "", false
	}
	return loc.Lang, true
}

// ChooseEditorLocale is the rule, with every input spelled out (see the file
// comment for the order and why). setting is the administrator's ("auto" or a
// language); requested the request's `lang`; acceptLanguage the request's
// Accept-Language header as sent; account the account's language;
// instanceDefault FILEX_DEFAULT_LOCALE.
//
// ⚠ A setting that is not a language ONLYOFFICE offers (a hand-edited row)
// counts as "auto": the person's own language is a better guess than English.
func ChooseEditorLocale(setting, requested, acceptLanguage, account, instanceDefault string) EditorLocale {
	if fixed, ok := NormalizeEditorLang(setting); ok && fixed != EditorLangAuto {
		if loc, ok := matchEditorLang(fixed); ok {
			return loc
		}
	}
	cands := make([]string, 0, 8)
	cands = append(cands, requested)
	cands = append(cands, acceptLanguageTags(acceptLanguage)...)
	cands = append(cands, account, instanceDefault)
	for _, c := range cands {
		if loc, ok := matchEditorLang(c); ok {
			return loc
		}
	}
	en := langByLower["en"]
	return EditorLocale{Lang: en.code, Region: en.region}
}

// EditorLocale is the language and regional setting an editor this request
// opens is drawn in. requested is the request's `lang` (query or body),
// acceptLanguage its Accept-Language header, user the caller (nil: nobody's
// account to ask).
//
// ⚠⚠ The ONE place this is decided. BuildConfigForNode asks it for every
// editor config, whichever screen asked for the editor; a second copy of the
// rule anywhere (a screen choosing the editor's language itself) would be the
// screen deciding what the server decides.
func (s *Service) EditorLocale(ctx context.Context, user *model.User, requested, acceptLanguage string) EditorLocale {
	setting := EditorLangAuto
	def := ""
	if s != nil {
		if s.LiveEditorLang != nil {
			setting = s.LiveEditorLang(ctx)
		}
		def = s.DefaultLocale
	}
	account := ""
	if user != nil {
		account = user.Locale
	}
	return ChooseEditorLocale(setting, requested, acceptLanguage, account, def)
}

// matchEditorLang finds the language ONLYOFFICE offers that a tag means, and
// the regional setting that goes with it. ok is false for a tag ONLYOFFICE
// has nothing for.
func matchEditorLang(tag string) (EditorLocale, bool) {
	subs := tagSubtags(tag)
	if len(subs) == 0 {
		return EditorLocale{}, false
	}
	primary := subs[0]
	script, region := "", ""
	for _, s := range subs[1:] {
		switch {
		case len(s) == 4 && isAlpha(s) && script == "" && region == "":
			script = s
		case region == "" && ((len(s) == 2 && isAlpha(s)) || (len(s) == 3 && isDigits(s))):
			region = s
		}
	}

	code := ""
	switch primary {
	case "pt":
		// ONLYOFFICE's `pt` is Brazil's. Every other Portuguese-speaking
		// country writes the European variant, which is `pt-PT`.
		if region == "" || region == "br" {
			code = "pt"
		} else {
			code = "pt-PT"
		}
	case "zh":
		switch {
		case script == "hant":
			code = "zh-TW"
		case script == "" && (region == "tw" || region == "hk" || region == "mo"):
			code = "zh-TW"
		default:
			code = "zh"
		}
	case "sr":
		if script == "cyrl" {
			code = "sr-Cyrl"
		} else {
			code = "sr"
		}
	case "nb", "nn", "no":
		code = "no"
	case "iw":
		code = "he"
	case "in":
		code = "id"
	default:
		code = primary
	}
	l, ok := langByLower[strings.ToLower(code)]
	if !ok {
		return EditorLocale{}, false
	}

	loc := EditorLocale{Lang: l.code, Region: l.region}
	if region != "" {
		for _, cand := range []string{
			strings.Join(subs, "-"),
			primary + "-" + script + "-" + region,
			primary + "-" + region,
		} {
			if r, ok := regionByLower[cand]; ok {
				loc.Region = r
				break
			}
		}
	}
	return loc, true
}

// tagSubtags splits a language tag into lower-case subtags ("pt_BR" and
// "pt-br" alike). Nothing for an empty value, "auto" or the wildcard.
func tagSubtags(tag string) []string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" || tag == "*" || tag == EditorLangAuto {
		return nil
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	var out []string
	for _, s := range strings.Split(tag, "-") {
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 || !isAlpha(out[0]) {
		return nil
	}
	return out
}

// acceptLanguageTags lists the tags an Accept-Language header names, in the
// order it names them (a browser writes them most wanted first; srvtext reads
// the header the same way), without the wildcard and without a tag the
// header refuses (q=0).
func acceptLanguageTags(h string) []string {
	var out []string
	for _, part := range strings.Split(h, ",") {
		fields := strings.Split(part, ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		refused := false
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if len(f) > 2 && (f[0] == 'q' || f[0] == 'Q') && f[1] == '=' {
				if q, err := strconv.ParseFloat(strings.TrimSpace(f[2:]), 64); err == nil && q <= 0 {
					refused = true
				}
			}
		}
		if !refused {
			out = append(out, tag)
		}
	}
	return out
}

func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
