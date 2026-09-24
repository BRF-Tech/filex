package srvtext

import (
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// byteUnits are the catalogue keys of the unit words, smallest first.
var byteUnits = []string{"server.unit.bytes", "server.unit.kb", "server.unit.mb", "server.unit.gb", "server.unit.tb", "server.unit.pb"}

// Bytes writes a byte count the way the interface does — the explorer's
// formatByteSize (packages/core/src/composables/useLocale.ts): base 1000,
// whole bytes, two decimals below 10 and one above (trailing zeros dropped),
// the language's own decimal mark, and the catalogue's unit words.
//
// ⚠ There were two server copies, both base 1024 with a Go "%.1f" (a dot in
// every language, English letters): the share e-mail and the no-JavaScript
// folder page. A file the listing called "1.5 MB" arrived in the mail as
// "1.4 MB", and the share dialog copied the server's rounding on purpose so
// the two would at least agree with each other (QA, 2026-09-21). One
// definition of a size now, on both sides of the wire.
func Bytes(lang string, n int64) string {
	if n < 0 {
		return "—"
	}
	idx := 0
	v := float64(n)
	for v >= 1000 && idx < len(byteUnits)-1 {
		v /= 1000
		idx++
	}
	unit := Text(lang, byteUnits[idx], nil)
	p := message.NewPrinter(tagOf(lang))
	if idx == 0 {
		return p.Sprint(number.Decimal(n, number.MaxFractionDigits(0))) + " " + unit
	}
	digits := 1
	if v < 10 {
		digits = 2
	}
	return p.Sprint(number.Decimal(v, number.MaxFractionDigits(digits))) + " " + unit
}
