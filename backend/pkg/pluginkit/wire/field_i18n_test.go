package wire

import (
	"encoding/json"
	"testing"
)

// A manifest setting may speak every language the app does (2026-09-21: the
// signing app's settings were English inside the Turkish admin panel,
// because a Field's label could only be one string).
func TestField_LabelsInEveryLanguage(t *testing.T) {
	raw := `{"key":"tsa","type":"bool",
		"label":{"en":"Add a time stamp","tr":"Her imzaya zaman damgası ekle"},
		"help":"English only",
		"placeholder":{"en":"https://…","tr":"https://… (boş: varsayılan)"},
		"options":[{"value":"a","label":{"en":"A","tr":"Â"}},{"value":"b","label":"B"}]}`
	var f Field
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if f.Label != "Add a time stamp" || f.Help != "English only" {
		t.Errorf("the strings read as English: %q %q", f.Label, f.Help)
	}
	if f.I18n == nil || f.I18n.Label["tr"] != "Her imzaya zaman damgası ekle" {
		t.Fatalf("the Turkish label was lost: %+v", f.I18n)
	}
	tr := f.Localized("tr")
	if tr.Label != "Her imzaya zaman damgası ekle" || tr.Help != "English only" || tr.Placeholder != "https://… (boş: varsayılan)" {
		t.Errorf("localized: %q / %q / %q", tr.Label, tr.Help, tr.Placeholder)
	}
	if tr.Options[0].Label != "Â" || tr.Options[1].Label != "B" {
		t.Errorf("options: %+v", tr.Options)
	}
	if f.Options[0].Label != "A" {
		t.Error("Localized must not write through to the original")
	}

	// Written back out as the maps it arrived as — the admin client resolves
	// {en, tr} itself, and a round trip must not flatten it to English.
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	if lm, ok := back["label"].(map[string]any); !ok || lm["tr"] != "Her imzaya zaman damgası ekle" {
		t.Errorf("label marshalled as %v", back["label"])
	}
	if back["help"] != "English only" {
		t.Errorf("a plain string stays a string: %v", back["help"])
	}
	opts := back["options"].([]any)
	if om, ok := opts[0].(map[string]any)["label"].(map[string]any); !ok || om["tr"] != "Â" {
		t.Errorf("option label marshalled as %v", opts[0])
	}

	// A field written the old way is byte-for-byte the old shape.
	var plain Field
	if err := json.Unmarshal([]byte(`{"key":"k","type":"string","label":"L"}`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.I18n != nil {
		t.Error("no map, no I18n")
	}
	b, _ := json.Marshal(plain)
	if string(b) != `{"key":"k","type":"string","label":"L"}` {
		t.Errorf("plain field marshalled as %s", b)
	}

	if err := json.Unmarshal([]byte(`{"key":"k","label":42}`), &plain); err == nil {
		t.Error("a number is not a label")
	}
}
