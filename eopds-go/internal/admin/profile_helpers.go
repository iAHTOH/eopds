package admin

import (
	"net/http"
	"strings"
)

// tr — функция перевода для языка запроса.
func (s *Server) tr(r *http.Request) func(string) string {
	return s.translatorFor(r)
}

// redirect выполняет 303 See Other на указанный путь.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// flash сохраняет одноразовое сообщение в cookie для страницы page.
// key — КЛЮЧ i18n (ASCII), а не готовый текст: значения cookie ограничены
// ASCII и не допускают кириллицу/пробелы, поэтому переводим при показе.
func (s *Server) flash(w http.ResponseWriter, r *http.Request, page, kind, key string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_" + page + "_" + kind,
		Value:    key,
		Path:     "/",
		MaxAge:   30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// takeFlash читает и «съедает» ключ сообщения из cookie («» если нет)
// и сбрасывает cookie в ответе w.
func takeFlash(w http.ResponseWriter, r *http.Request, page, kind string) string {
	name := "flash_" + page + "_" + kind
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	return c.Value
}

// setThemeCookie запоминает тему в cookie (light/dark).
func setThemeCookie(w http.ResponseWriter, theme string) {
	if !isKnownTheme(theme) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "theme", Value: theme, Path: "/", MaxAge: 31536000,
		SameSite: http.SameSiteLaxMode,
	})
}

// setLangCookie запоминает язык интерфейса в cookie (ru/en).
func setLangCookie(w http.ResponseWriter, lang string) {
	lang = strings.TrimSpace(lang)
	if !isUILang(lang) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "lang", Value: lang, Path: "/", MaxAge: 31536000,
		SameSite: http.SameSiteLaxMode,
	})
}
