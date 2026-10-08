package onlyoffice

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// The formats a save is kept in beside the file (besideTypes) are the ones the
// desktop app looks for in its working folder (desktop/src/openwith.ts
// BESIDE_FORMATS): both are held to one list in the shared drift file
// (filex #211, audit B20; web/tests/lib/serverRuleVectors.test.ts reads the
// desktop's side).
func TestRuleVectors_BesideFormats(t *testing.T) {
	raw, err := os.ReadFile("../api/handlers/testdata/rule-mirrors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Beside []string `json:"beside_formats"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var have []string
	for ext, ok := range besideTypes {
		if ok {
			have = append(have, ext)
		}
	}
	sort.Strings(have)
	want := append([]string(nil), v.Beside...)
	sort.Strings(want)
	if len(have) != len(want) {
		t.Fatalf("besideTypes = %v, the drift file says %v", have, want)
	}
	for i := range have {
		if have[i] != want[i] {
			t.Fatalf("besideTypes = %v, the drift file says %v", have, want)
		}
	}
}
