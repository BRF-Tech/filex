package handlers

import (
	"fmt"
	"math"
)

// Accent-filled buttons stay readable and visible in both themes (issue #29).
//
// "If I select OIDC login button black color almost disappearing in background
// of login frame … Also applies to white color for button it completely
// invisible". The operator's accent was used as a fill with a fixed label
// colour and no edge, so a white accent drew white-on-white and a near-black
// one vanished into a dark card.
//
// ⚠ The SAME rule, with the same thresholds, lives in the SPA for the sign-in
// page (web/src/lib/accentButton.ts). Change both or neither.

const (
	accentOnLightText  = "#15171c"
	accentOnDarkText   = "#ffffff"
	accentMinEdgeRatio = 1.6
	accentEdgeOnLight  = "rgba(21,23,28,0.35)"
	accentEdgeOnDark   = "rgba(255,255,255,0.45)"
	publicCardLightHex = "#ffffff" // --px-card, light
	publicCardDarkHex  = "#1f242c" // --px-card, dark
)

// accentLuminance is the WCAG relative luminance of a validated hex colour.
func accentLuminance(hexColor string) float64 {
	r, g, b := brandingRGB(hexColor)
	lin := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// accentContrast is the WCAG contrast ratio between two hex colours.
func accentContrast(a, b string) float64 {
	la, lb := accentLuminance(a), accentLuminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// accentOnText is the label colour for a button filled with accent.
func accentOnText(accent string) string {
	if accentContrast(accent, accentOnDarkText) >= accentContrast(accent, accentOnLightText) {
		return accentOnDarkText
	}
	return accentOnLightText
}

// accentEdge is the edge a button filled with accent draws on a card of the
// given colour: the fill itself when it already stands out, a visible line
// in the card's theme when it does not.
func accentEdge(accent, card string, dark bool) string {
	if accentContrast(accent, card) >= accentMinEdgeRatio {
		return accent
	}
	if dark {
		return accentEdgeOnDark
	}
	return accentEdgeOnLight
}

// accentButtonCSS is the custom-property block the public pages read for an
// accent-filled button: the label colour and a per-theme edge.
func accentButtonCSS(accent string) string {
	return fmt.Sprintf(
		`<style>:root{--px-on-accent:%s;--px-accent-edge:%s}@media (prefers-color-scheme: dark){:root{--px-accent-edge:%s}}</style>`,
		accentOnText(accent),
		accentEdge(accent, publicCardLightHex, false),
		accentEdge(accent, publicCardDarkHex, true),
	)
}
