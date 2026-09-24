package handlers

/* ===== tema:v1 — the escape hatch: operator raw CSS =====

Beside the theme editor (themes.go), which is the tool almost everybody should
use, there is one settings row holding a stylesheet the operator pastes in.
Tokens cannot express "put our watermark behind the file list"; CSS can. CSS
can also do every bad thing described in this comment, which is why this file
is mostly guards.

⚠⚠ THIS IS A REWRITE OF AN EXISTING FEATURE, AND THE OLD SHAPE WAS UNSAFE.
`ui.custom_css` used to ride the PUBLIC `/api/branding` payload and was applied
to every surface, including the login page and the admin screen that edits it.
That was three of the four things this round had to fix. What changed:

 1. IT IS OFF BY DEFAULT.  A second row, `ui.custom_css_enabled`, gates it, and
    an installation that had a sheet before this version must switch it back on
    deliberately. There is no grandfathering, and that is the point: the
    semantics changed under the operator's feet (the login page no longer wears
    it; url() no longer fetches), so an explicit re-opt-in is the honest
    migration rather than a surprise.

    ⚠ A SETTINGS SWITCH AND NOT AN ENV FLAG, deliberately. The person who needs
    to turn this off is the person who has just pasted something ruinous, and
    what they need is one click on a screen that still works — not shell access
    to the host, a compose edit and a restart. An env flag moves the off switch
    further away from the failure exactly when it is needed most. The admin
    screen is immune by construction (point 2), so that click is always
    reachable.

 2. IT CANNOT REACH THE SCREEN THAT TURNS IT OFF, two ways over.
    - The admin Appearance route REMOVES the <style> element while it is open
      (web/src/lib/customCss.ts `suspendCustomCss`). Nothing a stylesheet can
      express survives not being in the document.
    - Everything served is wrapped in `@scope (:root) to (.fe-css-immune)`, so
      an operator selector cannot MATCH inside any subtree marked immune — the
      Appearance panel and destructive confirmation dialogs carry that class.
      This is the part that protects surfaces the route switch cannot.

 3. IT NEVER REACHES AN ANONYMOUS VISITOR.  It is served from
    `GET /api/me/custom-css`, behind auth, instead of from the public branding
    payload. The login page, share pages, the PIN gate and signing pages
    therefore cannot wear it at all — they follow the instance THEME, which is
    a token set and cannot lie (themes.go).

 4. IT CANNOT PHONE HOME.  `SanitizeCustomCSS` strips `@import` outright and
    rewrites every `url()` that is not a `data:` URI or a same-document
    `#fragment` into an inert `url("data:,")`. That closes the CSS
    exfiltration classic — an attribute selector plus a background image that
    reports which page, which filename or which keystroke a viewer is on —
    because after this pass the sheet has no way to originate a request at all.

 5. NEVER A TENANT ADMIN.  `allowSettingWrite` (settings.go) already refuses
    every key that is not `branding.*` to a confined tenant admin, and both of
    these keys are `ui.*`. The refusal is tested rather than assumed
    (custom_css_test.go), because the guarantee rests on a key NOT being in a
    list — the kind of thing that stays true only while somebody checks.

WHAT A DETERMINED PAYLOAD CAN STILL DO — the short version is that scoping
limits which elements a rule can MATCH and cannot un-apply an inherited or
ancestor-level property. A sheet that writes `:root{display:none}` blanks the
app outside the immune subtrees, and within the surfaces it does reach it can
still restyle, cover or relabel controls. The answers to that are the immune
screen, the off switch, and the fact that only the instance operator can write
it. */

import (
	"errors"
	"fmt"
	"strings"
)

// CustomCSSSettingKey is the settings row holding the operator stylesheet.
const CustomCSSSettingKey = "ui.custom_css"

// CustomCSSEnabledSettingKey is the switch. Absent, or anything other than a
// true-ish string, means OFF — the direction that makes a missing row safe.
const CustomCSSEnabledSettingKey = "ui.custom_css_enabled"

// CustomCSSImmuneClass marks a subtree the operator stylesheet may not match
// inside. The admin Appearance panel and destructive confirmations carry it.
//
// ⚠ A plain class and not a data attribute, so a single `@scope` limit can
// name it; `fe-`prefixed like every other class the packages own.
const CustomCSSImmuneClass = "fe-css-immune"

// CustomCSSMaxBytes caps the stored sheet. 64 KiB is roughly ten times the
// largest hand-written token override we ship, and small enough that a sheet
// with Turkish comments in it is never what makes a page slow. The admin page
// shows the same number and counts UTF-8 bytes, so the two agree.
const CustomCSSMaxBytes = 64 * 1024

// SanitizedCSS is the result of a guard pass over an operator stylesheet.
type SanitizedCSS struct {
	// Hoisted holds the top-level `@font-face` and `@keyframes` blocks, which
	// are emitted OUTSIDE the scope wrapper.
	//
	// ⚠ They have to be hoisted or they stop working: both at-rules define a
	// NAME rather than matching elements, and their behaviour inside `@scope`
	// is at best inconsistent across engines. Hoisting is safe precisely
	// because the url() pass has already run — a hoisted `@font-face` can only
	// reference a `data:` URI, so moving it out of the scope cannot give it a
	// network request it did not already lack.
	Hoisted string
	// Scoped holds everything else, to be wrapped in the `@scope` rule.
	Scoped string
	// Removed names what the pass took out, so the admin screen can say so
	// instead of silently serving something other than what was typed.
	Removed []string
}

// Empty reports whether there is nothing to serve.
func (s SanitizedCSS) Empty() bool {
	return strings.TrimSpace(s.Hoisted) == "" && strings.TrimSpace(s.Scoped) == ""
}

// Render produces the text injected into the browser's <style> element.
func (s SanitizedCSS) Render() string {
	if s.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("/* filex operator stylesheet */\n")
	if h := strings.TrimSpace(s.Hoisted); h != "" {
		b.WriteString(h)
		b.WriteString("\n")
	}
	if sc := strings.TrimSpace(s.Scoped); sc != "" {
		// ⚠ The `to (…)` half is the whole guard. `@scope (:root)` alone would
		// be a no-op — every element is inside :root. The limit is what carves
		// the immune subtrees out of the match set.
		//
		// ⚠ An engine without @scope support drops this block entirely, so the
		// stylesheet does nothing. That is the correct way for this feature to
		// fail: a plain-looking panel, not an unguarded one.
		fmt.Fprintf(&b, "@scope (:root) to (.%s) {\n%s\n}\n", CustomCSSImmuneClass, sc)
	}
	return b.String()
}

// customCSSEnabled reads the switch out of a settings map. Absent = off.
func customCSSEnabled(m map[string]string) bool {
	return brandingBool(m[CustomCSSEnabledSettingKey])
}

// customCSSFromSettings reads the operator stylesheet out of a settings map,
// already sanitised and rendered, returning "" when the switch is off.
//
// ⚠ It sanitises on the way OUT as well as on the way in. The stored row went
// through the same pass when it was written, but a row can also arrive from an
// older version of this file, from a future one, or from somebody with a
// database client — and this is the function whose output reaches a browser.
//
// Instance-wide on purpose: unlike branding.*, there is no tenant.<id> overlay,
// because one installation renders one look — and because a tenant admin may
// not write it in the first place.
func customCSSFromSettings(m map[string]string) string {
	if !customCSSEnabled(m) {
		return ""
	}
	clean, err := SanitizeCustomCSS(strings.TrimSpace(m[CustomCSSSettingKey]))
	if err != nil {
		return ""
	}
	return clean.Render()
}

// validateCustomCSS checks an operator stylesheet before it is persisted.
// Empty always passes (clearing the field).
func validateCustomCSS(value string) error {
	_, err := SanitizeCustomCSS(value)
	return err
}

/* ------------------------------------------------------------------ */
/* The sanitiser                                                       */
/* ------------------------------------------------------------------ */

// SanitizeCustomCSS runs the guard pass over an operator stylesheet.
//
// It is a small CSS-aware scanner rather than a set of regexes, and it has to
// be: a regex for `url\(` cannot tell the one in a comment from the one in a
// declaration, and a regex that counts braces cannot tell the `}` inside a
// quoted string from the one that closes a rule. Both differences are
// load-bearing — the brace count is what stops a sheet from ending the
// `@scope` wrapper early and escaping the guard, which is the single most
// important thing this function does.
//
// The pass:
//
//   - comments are COPIED THROUGH but never scanned inside (an operator's
//     notes are theirs, and nothing in a comment is fetched or matched)
//   - quoted strings are copied verbatim, and braces inside them do not count
//   - `@import` is removed, at-rule and all
//   - `url(…)` and `image-set(…)` that are not `data:` or `#fragment` are made
//     inert
//   - top-level `@font-face` / `@keyframes` are hoisted out of the scope
//   - the sheet is refused if its braces do not balance, if it contains
//     `</style`, or if it is over the byte cap
func SanitizeCustomCSS(raw string) (SanitizedCSS, error) { return sanitizeCSS(raw, true) }

// sanitizeCSS is the pass itself. `hoist` is false for the recursive call that
// cleans an already-extracted `@font-face` / `@keyframes` block.
//
// ⚠⚠ THE FLAG IS WHY THIS TERMINATES. The first version recursed with the
// block's own text still starting at its `@font-face`, so the recursive call
// matched the same branch and called itself again on the same bytes — a stack
// overflow reachable from the admin settings form by pasting one @font-face
// rule. The flag makes the second pass structurally unable to recurse a third
// time.
func sanitizeCSS(raw string, hoist bool) (SanitizedCSS, error) {
	var out SanitizedCSS
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if len(raw) > CustomCSSMaxBytes {
		return out, fmt.Errorf("the stylesheet is capped at %d KB", CustomCSSMaxBytes/1024)
	}
	// The SPA sets this as a style element's textContent, which an HTML parser
	// never re-reads, so this cannot break out there. It is refused anyway
	// because "</style" is the one string that would end a <style> block if
	// this value ever reached a server-rendered page, and a stylesheet has no
	// reason to contain it.
	if strings.Contains(strings.ToLower(raw), "</style") {
		return out, errors.New(`a stylesheet has no reason to contain "</style"`)
	}

	var (
		scoped  strings.Builder
		hoisted strings.Builder
		removed = map[string]bool{}
		depth   int
		i       int
	)
	dst := &scoped

	n := len(raw)
	for i < n {
		c := raw[i]

		// Comments: copy through, do not scan inside.
		if c == '/' && i+1 < n && raw[i+1] == '*' {
			end := strings.Index(raw[i+2:], "*/")
			if end < 0 {
				dst.WriteString(raw[i:])
				i = n
				break
			}
			stop := i + 2 + end + 2
			dst.WriteString(raw[i:stop])
			i = stop
			continue
		}

		// Strings: copy verbatim, honouring backslash escapes, so a brace or a
		// comment marker inside one is inert.
		if c == '"' || c == '\'' {
			j := i + 1
			for j < n {
				if raw[j] == '\\' {
					j += 2
					continue
				}
				if raw[j] == c {
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			dst.WriteString(raw[i:j])
			i = j
			continue
		}

		if c == '@' {
			switch atRuleName(raw, i) {
			case "import":
				// Removed at ANY depth. `@import` nested in a block is invalid
				// CSS and would be dropped by the parser anyway, but "the
				// parser would probably drop it" is not a guarantee.
				i = skipAtStatement(raw, i)
				removed["@import"] = true
				continue
			case "font-face", "keyframes", "-webkit-keyframes":
				if hoist && depth == 0 {
					stop := skipBalancedBlock(raw, i)
					if stop < 0 {
						return out, errors.New("the stylesheet has an unclosed block")
					}
					// Clean the extracted block with a NON-hoisting pass, so
					// its own url() calls are guarded and it cannot match this
					// branch again.
					frag, err := sanitizeCSS(raw[i:stop], false)
					if err != nil {
						return out, err
					}
					for _, r := range frag.Removed {
						removed[r] = true
					}
					hoisted.WriteString(strings.TrimSpace(frag.Scoped))
					hoisted.WriteString("\n")
					i = stop
					continue
				}
			}
		}

		// url(…) — with @import above, the only way a stylesheet can originate
		// a request.
		if (c == 'u' || c == 'U') && !identByte(prevByte(raw, i)) && hasFoldedPrefix(raw[i:], "url(") {
			stop, target := readFunctionArg(raw, i+len("url("))
			if stop < 0 {
				return out, errors.New("the stylesheet has an unclosed url(")
			}
			if safeCSSTarget(target) {
				dst.WriteString(raw[i:stop])
			} else {
				dst.WriteString(`url("data:,")`)
				removed["url("+truncateForMessage(target)+")"] = true
			}
			i = stop
			continue
		}

		// image-set(…) accepts a BARE STRING as a URL — `image-set("a.png" 1x)`
		// fetches without ever spelling url(). Rather than teach the scanner
		// that grammar, the whole function is replaced: it is rare in a
		// hand-written brand sheet, and `url("data:…")` covers what it was for.
		if (c == 'i' || c == 'I' || c == '-') && !identByte(prevByte(raw, i)) &&
			(hasFoldedPrefix(raw[i:], "image-set(") || hasFoldedPrefix(raw[i:], "-webkit-image-set(")) {
			open := strings.IndexByte(raw[i:], '(')
			stop, _ := readFunctionArg(raw, i+open+1)
			if stop < 0 {
				return out, errors.New("the stylesheet has an unclosed image-set(")
			}
			dst.WriteString("none")
			removed["image-set()"] = true
			i = stop
			continue
		}

		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth < 0 {
				// ⚠⚠ THE MOST IMPORTANT REFUSAL IN THIS FILE. An extra closing
				// brace would end the `@scope` wrapper early and leave the rest
				// of the sheet OUTSIDE the guard, free to match inside the
				// immune subtrees. There is no way to write that by accident in
				// a stylesheet that was going to work anyway.
				return out, errors.New("the stylesheet has one closing brace too many")
			}
		}
		dst.WriteByte(c)
		i++
	}
	if depth != 0 {
		return out, errors.New("the stylesheet has an unclosed block")
	}

	out.Hoisted = hoisted.String()
	out.Scoped = scoped.String()
	for r := range removed {
		out.Removed = append(out.Removed, r)
	}
	sortStrings(out.Removed)
	return out, nil
}

// safeCSSTarget reports whether a url() target can be served without any
// network request: an inline `data:` payload, or a same-document `#fragment`
// (what an SVG filter or gradient reference is).
func safeCSSTarget(target string) bool {
	t := strings.TrimSpace(target)
	t = strings.Trim(t, "\"'")
	t = strings.TrimSpace(t)
	if t == "" {
		return true
	}
	lower := strings.ToLower(t)
	return strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "#")
}

// atRuleName returns the lowercase at-rule identifier starting at s[i]=='@'.
func atRuleName(s string, i int) string {
	j := i + 1
	for j < len(s) && (identByte(s[j]) || s[j] == '-') {
		j++
	}
	return strings.ToLower(s[i+1 : j])
}

// skipAtStatement returns the index just past an at-rule statement — the byte
// after its terminating `;`, after its balanced block, or the end.
func skipAtStatement(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '"', '\'':
			q := s[j]
			j++
			for j < len(s) && s[j] != q {
				if s[j] == '\\' {
					j++
				}
				j++
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth <= 0 {
				return j + 1
			}
		case ';':
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(s)
}

// skipBalancedBlock returns the index just past the block that starts at or
// after i, or -1 when it never closes.
func skipBalancedBlock(s string, i int) int {
	depth := 0
	seen := false
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '"', '\'':
			q := s[j]
			j++
			for j < len(s) && s[j] != q {
				if s[j] == '\\' {
					j++
				}
				j++
			}
		case '{':
			depth++
			seen = true
		case '}':
			depth--
			if seen && depth == 0 {
				return j + 1
			}
		}
	}
	return -1
}

// readFunctionArg reads from just after an opening paren to the matching close,
// returning the index past the ')' and the raw argument text.
func readFunctionArg(s string, i int) (int, string) {
	depth := 1
	start := i
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '"', '\'':
			q := s[j]
			j++
			for j < len(s) && s[j] != q {
				if s[j] == '\\' {
					j++
				}
				j++
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j + 1, s[start:j]
			}
		}
	}
	return -1, ""
}

// hasFoldedPrefix is strings.HasPrefix, case-insensitively, for ASCII.
func hasFoldedPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}

// identByte reports whether b can appear inside a CSS identifier — used to
// make sure `url(` is a function and not the tail of `my-url(`.
func identByte(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// prevByte returns the byte before i, or a space at the start of the string.
func prevByte(s string, i int) byte {
	if i == 0 {
		return ' '
	}
	return s[i-1]
}

// truncateForMessage shortens a target for the "what was removed" list, which
// is shown to the operator and must not become a wall of base64.
func truncateForMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}

// sortStrings is a tiny insertion sort — the list is at most a handful of
// entries and reaching for the sort package for it would be the larger cost.
func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

/* ===== /tema:v1 ===== */
