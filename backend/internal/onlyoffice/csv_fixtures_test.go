package onlyoffice

// The CSV fixtures of the callback's tests: the rules a save is written by are
// internal/officesave's and tested there; these drive them through the
// document server's callback.

import "strings"

// ds is a CSV as Docs 9.4 saves it: a byte order mark, the rows
// (comma-separated by the caller) with "\n" between them and after the last.
func ds(rows ...string) string { return "\xEF\xBB\xBF" + strings.Join(rows, "\n") + "\n" }

// ds10 is a CSV as Docs 10 saves it: the file's own delimiter (the caller's
// rows), "\n" between them and after the last. Whether it puts a byte order
// mark in front is not measured: both are read (officesave readSaved).
func ds10(bom bool, rows ...string) string {
	s := strings.Join(rows, "\n") + "\n"
	if bom {
		return "\xEF\xBB\xBF" + s
	}
	return s
}

func crlf(rows ...string) string { return strings.Join(rows, "\r\n") + "\r\n" }

// The file of the measurement (production, 0.51.0): semicolons, CRLF, UTF-8,
// no byte order mark, a final line end. The same as officesave's.
var measuredOriginal = crlf(
	"Ad;Telefon;Kimlik No;Kart No;Tarih;Tutar;Kod;Not",
	"Çağrı Öztürk;05320000001;12345678901;1234567890123456;01.02.2026;3,5;007;ilk satır",
	"Şule Iğdır;05330000002;10987654321;9999888877776666;15.03.2026;1250,75;042;ığüşöç İĞÜŞÖÇ",
	"Deneme Üç;05050000003;11111111111;4000000000000002;31.12.2025;0,5;000;boş değil",
)

// What ONLYOFFICE saved for it after one cell was edited (`ilk satır` to
// `degisti`) and a note typed into a new row two rows below.
var measuredSaved = ds(
	"Ad,Telefon,Kimlik No,Kart No,Tarih,Tutar,Kod,Not",
	"Çağrı Öztürk,5320000001,12345678901,1234567890123456,1/2/2026,\"3,5\",7,degisti,",
	"Şule Iğdır,5330000002,10987654321,9999888877776666,15.03.2026,\"1250,75\",42,ığüşöç İĞÜŞÖÇ,",
	"Deneme Üç,5050000003,11111111111,4000000000000002,31.12.2025,\"0,5\",0,boş değil,",
	"",
	",bunu deneme için masaüstünden girip yazıyorum",
)

var measuredWant = crlf(
	"Ad;Telefon;Kimlik No;Kart No;Tarih;Tutar;Kod;Not",
	"Çağrı Öztürk;05320000001;12345678901;1234567890123456;01.02.2026;3,5;007;degisti",
	"Şule Iğdır;05330000002;10987654321;9999888877776666;15.03.2026;1250,75;042;ığüşöç İĞÜŞÖÇ",
	"Deneme Üç;05050000003;11111111111;4000000000000002;31.12.2025;0,5;000;boş değil",
	"",
	";bunu deneme için masaüstünden girip yazıyorum",
)
