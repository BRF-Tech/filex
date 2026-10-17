package wasmplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLanguageRows_OnlyCountsApplicablePluralForms(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		words map[string]string
		want  LanguageRow
	}{
		{"Chinese does not need English singulars", "zh", map[string]string{"files": "文件", "server.items": "项目"}, LanguageRow{Keys: 2, Translated: 2, Total: 2, Percent: 100}},
		{"regional Chinese uses the same rules", "zh-Hans-CN", map[string]string{"files": "文件", "server.items": "项目"}, LanguageRow{Keys: 2, Translated: 2, Total: 2, Percent: 100}},
		{"Japanese does not need English singulars", "ja", map[string]string{"files": "ファイル", "server.items": "項目"}, LanguageRow{Keys: 2, Translated: 2, Total: 2, Percent: 100}},
		{"unused translated singular cannot inflate coverage", "zh", map[string]string{"files": "文件", "files_one": "文件", "server.items": "项目", "server.items_one": "项目"}, LanguageRow{Keys: 4, Translated: 2, Total: 2, Percent: 100}},
		{"missing applicable text still lowers coverage", "zh", map[string]string{"files": "文件", "files_one": "文件", "server.items": "  "}, LanguageRow{Keys: 2, Translated: 1, Total: 2, Percent: 50}},
		{"English still needs singulars", "en", map[string]string{"files": "files", "server.items": "items"}, LanguageRow{Keys: 2, Translated: 2, Total: 4, Percent: 50}},
		{"Russian extra forms retain their existing treatment", "ru", map[string]string{"files": "файлы", "files_one": "файл", "files_few": "файла", "server.items": "элементы", "server.items_one": "элемент", "typo": "ошибка"}, LanguageRow{Keys: 6, Translated: 4, Unknown: 1, Total: 4, Percent: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &Registry{}
			reg.SetCatalogue([]string{"files", "files_one", "server.items", "server.items_one"})
			tt.want.Code = tt.code
			m := &Manifest{}
			m.UILocales = map[string]map[string]string{tt.code: tt.words}
			assert.Equal(t, []LanguageRow{tt.want}, reg.LanguageRows(m))
		})
	}
}

func TestLanguageRows_PluralSuffixWithoutBaseIsStillRequired(t *testing.T) {
	reg := &Registry{}
	reg.SetCatalogue([]string{"standalone_one", "title"})
	m := &Manifest{}
	m.UILocales = map[string]map[string]string{"zh": {"title": "标题"}}
	rows := reg.LanguageRows(m)
	assert.Equal(t, []LanguageRow{{Code: "zh", Keys: 1, Translated: 1, Total: 2, Percent: 50}}, rows)
}
