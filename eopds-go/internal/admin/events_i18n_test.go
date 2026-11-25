package admin

import (
	"regexp"
	"sort"
	"testing"
)

// Категории событий, которые пишет код (см. вызовы logEvent/logEventUser).
// Список поддерживается вручную: если добавили событие — добавьте категорию
// здесь. Тест гарантирует, что для неё есть перевод ev.<category> в ru и en,
// иначе в «Сообщениях системы» пользователь увидит сырой ключ.
var knownEventCategories = []string{
	"auth.login",
	"auth.login_failed",
	"auth.logout",
	"book.comment",
	"book.comment_delete",
	"book.download",
	"book.rate",
	"lib.clear",
	"mail.error",
	"mail.log_cleared",
	"mail.password_reset_done",
	"mail.password_reset_requested",
	"mail.reset_rate_limited",
	"mail.sent",
	"mail.settings_saved",
	"mail.tokens_purged",
	"mail.verify_failed",
	"mail.verify_ok",
	"profile.avatar",
	"profile.logout_others",
	"profile.password",
	"profile.update",
	"scan.end",
	"scan.error",
	"scan.start",
	"settings.scan_dirs",
}

// Каждая категория события должна иметь перевод на всех языках интерфейса.
// Регрессия: события profile.* писались в журнал, но переводов не было —
// на дашборде показывался ключ ev.profile.update.
func TestEventCategoriesTranslated(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, lang := range []string{"ru", "en"} {
		tr := srv.i18n.Translator(lang)
		for _, cat := range knownEventCategories {
			key := "ev." + cat
			if got := tr(key); got == key || got == "" {
				t.Errorf("нет перевода %q для языка %q", key, lang)
			}
		}
	}
}

// Плейсхолдеры {name} в шаблоне события должны использоваться согласованно:
// каждый плейсхолдер есть и в ru-, и в en-варианте. Расхождение означает
// опечатку, из-за которой подстановка не сработает на одном из языков.
func TestEventPlaceholdersConsistent(t *testing.T) {
	srv, _ := newTestServer(t)
	ru := srv.i18n.Translator("ru")
	en := srv.i18n.Translator("en")
	re := regexp.MustCompile(`\{([a-z_]+)\}`)

	for _, cat := range knownEventCategories {
		key := "ev." + cat
		ruVars := placeholders(re.FindAllStringSubmatch(ru(key), -1))
		enVars := placeholders(re.FindAllStringSubmatch(en(key), -1))
		if len(ruVars) != len(enVars) {
			t.Errorf("%s: наборы плейсхолдеров различаются: ru=%v en=%v",
				key, ruVars, enVars)
			continue
		}
		for i := range ruVars {
			if ruVars[i] != enVars[i] {
				t.Errorf("%s: плейсхолдеры различаются: ru=%v en=%v",
					key, ruVars, enVars)
				break
			}
		}
	}
}

// placeholders возвращает отсортированный уникальный список имён.
func placeholders(matches [][]string) []string {
	var out []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		out = append(out, m[1])
	}
	sort.Strings(out)
	// Уберём дубликаты (плейсхолдер может встречаться дважды).
	uniq := out[:0]
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			uniq = append(uniq, v)
		}
	}
	return uniq
}
