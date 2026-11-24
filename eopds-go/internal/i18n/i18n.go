// Package i18n — простой слой локализации для Go-ядра EOPDS.
//
// Единый источник истины — translations.json (плоская структура
// {lang: {msgid: text}}), встраиваемый через go:embed. Без .po/.mo.
// Список языков — LINGUAS.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed translations.json LINGUAS
var files embed.FS

// DefaultLang — язык по умолчанию (на него откатываемся при отсутствии перевода).
const DefaultLang = "en"

// Bundle — загруженные переводы.
type Bundle struct {
	langs []string
	msgid string                       // язык, в котором хранятся ключи msgid
	table map[string]map[string]string // lang -> msgid -> text
}

// Load читает встроенные переводы.
func Load() (*Bundle, error) {
	raw, err := files.ReadFile("translations.json")
	if err != nil {
		return nil, fmt.Errorf("i18n: read translations: %w", err)
	}
	var data map[string]map[string]string
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("i18n: parse translations: %w", err)
	}

	b := &Bundle{msgid: DefaultLang, table: data}
	// Языки берём из LINGUAS (если есть), иначе — из ключей JSON.
	if lr, err := files.ReadFile("LINGUAS"); err == nil {
		for _, line := range strings.Split(string(lr), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if _, ok := data[line]; ok {
				b.langs = append(b.langs, line)
			}
		}
	}
	if len(b.langs) == 0 {
		for l := range data {
			b.langs = append(b.langs, l)
		}
		sort.Strings(b.langs)
	}
	return b, nil
}

// Languages возвращает доступные языки (в порядке LINGUAS).
func (b *Bundle) Languages() []string { return append([]string(nil), b.langs...) }

// Has сообщает, поддерживается ли язык.
func (b *Bundle) Has(lang string) bool {
	_, ok := b.table[lang]
	return ok
}

// Translator возвращает функцию перевода для указанного языка.
func (b *Bundle) Translator(lang string) func(string) string {
	if !b.Has(lang) {
		lang = DefaultLang
	}
	primary := b.table[lang]
	fallback := b.table[DefaultLang]
	return func(msgid string) string {
		if s, ok := primary[msgid]; ok && s != "" {
			return s
		}
		if s, ok := fallback[msgid]; ok && s != "" {
			return s
		}
		return msgid
	}
}
