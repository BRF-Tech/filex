package officesavetest

// A decimal typed into a column whose decimals the file writes with a comma
// (officesave csv_keep.go → csvDecimalCommas), measured on Docs 9.4.0.1
// (onlyoffice/documentserver:9.4.0.1, JWT off, its defaults), 2026-10-17, the
// way the vectors of docs94.go were taken: the editor in the language and
// region filex passes (tr / tr-TR, en / en-US, de / de-DE).

import "strings"

// Docs94CommaOriginal is the file of the report (olivov, filex 0.56.3 with
// Docs 9.4.0-129, the editor in Turkish): invented data, semicolons, CRLF, no
// byte order mark. E2 `3,5` typed over as `4,5`, nothing else touched; filex
// 0.56.3 wrote `4.5`.
const Docs94CommaOriginal = "Ad;Telefon;Kod;Tarih;Tutar;Hesap\r\n" +
	"Ayşe Yılmaz;05320000001;007;01.02.2026;3,5;TR000000000000000000000001\r\n" +
	"Mehmet Demir;05330000002;042;15.03.2026;1250,75;TR000000000000000000000002\r\n" +
	"Can Kaya;05340000003;000;28.02.2026;0,25;TR000000000000000000000003\r\n"

var docs94CommaEdited = strings.Replace(Docs94CommaOriginal, ";3,5;", ";4,5;", 1)

// Docs94CommaSaved is what each editor saved for it, and what the file gets.
var Docs94CommaSaved = map[string]Saved{
	"tr": {
		Saved: "\xEF\xBB\xBFAd,Telefon,Kod,Tarih,Tutar,Hesap\n" +
			"Ayşe Yılmaz,5320000001,7,1.02.2026,4.5,TR000000000000000000000001\n" +
			"Mehmet Demir,5330000002,42,15.03.2026,\"1250,75\",TR000000000000000000000002\n" +
			"Can Kaya,5340000003,0,28.02.2026,\"0,25\",TR000000000000000000000003\n",
		Want: docs94CommaEdited,
	},
	"de": {
		Saved: "\xEF\xBB\xBFAd,Telefon,Kod,Tarih,Tutar,Hesap\n" +
			"Ayşe Yılmaz,5320000001,7,01.02.2026,4.5,TR000000000000000000000001\n" +
			"Mehmet Demir,5330000002,42,15.03.2026,\"1250,75\",TR000000000000000000000002\n" +
			"Can Kaya,5340000003,0,28.02.2026,\"0,25\",TR000000000000000000000003\n",
		Want: docs94CommaEdited,
	},
	// The English editor reads the typed `4,5` as text, and the save has it
	// as typed: the comma was never lost there.
	"en": {
		Saved: "\xEF\xBB\xBFAd,Telefon,Kod,Tarih,Tutar,Hesap\n" +
			"Ayşe Yılmaz,5320000001,7,1/2/2026,\"4,5\",TR000000000000000000000001\n" +
			"Mehmet Demir,5330000002,42,15.03.2026,\"1250,75\",TR000000000000000000000002\n" +
			"Can Kaya,5340000003,0,28.02.2026,\"0,25\",TR000000000000000000000003\n",
		Want: docs94CommaEdited,
	},
	// A second session in Turkish: E4 `0,25` typed over as `1,1`, a row
	// added below with `2,75` in its Tutar (and no date).
	"tr, a row added": {
		Saved: "\xEF\xBB\xBFAd,Telefon,Kod,Tarih,Tutar,Hesap\n" +
			"Ayşe Yılmaz,5320000001,7,1.02.2026,\"3,5\",TR000000000000000000000001\n" +
			"Mehmet Demir,5330000002,42,15.03.2026,\"1250,75\",TR000000000000000000000002\n" +
			"Can Kaya,5340000003,0,28.02.2026,1.1000000000000001,TR000000000000000000000003\n" +
			"Zeynep Ak,5350000004,9,,2.75\n",
		Want: strings.Replace(Docs94CommaOriginal, ";0,25;", ";1,1;", 1) + "Zeynep Ak;5350000004;9;;2,75\r\n",
	},
}
