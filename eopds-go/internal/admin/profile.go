package admin

import (
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Ограничения аватара и шрифта читалки.
const (
	avatarMaxBytes  = 4 << 20 // 4 МиБ до обработки
	avatarMaxSide   = 512     // максимальная сторона после обрезки, px
	avatarJPEGQual  = 88
	fontSizeMin     = 50 // %
	fontSizeMax     = 300
	defaultFontSize = 100
)

// Допустимые предпочтения (валидация).
var (
	validPrefLangs  = map[string]bool{"": true, "ru": true, "en": true}
	validPrefThemes = map[string]bool{"": true, "light": true, "dark": true}
	validReadFonts  = map[string]bool{"": true, "serif": true, "sans": true, "mono": true}
)

// avatarDir возвращает каталог аватаров (<ConfigDir>/avatars), создавая его.
func (s *Server) avatarDir() (string, error) {
	dir := filepath.Join(s.cfg.ConfigDir, "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// avatarFilename — стабильное имя файла аватара пользователя.
func avatarFilename(userID int64) string {
	return "u" + strconv.FormatInt(userID, 10) + ".jpg"
}

// avatarURL — URL аватара пользователя (” если нет).
func avatarURL(u *store.User) string {
	if u == nil || u.Avatar == "" {
		return ""
	}
	return "/settings/avatar?u=" + strconv.FormatInt(u.ID, 10)
}

// handleProfile показывает страницу профиля (GET /settings/profile).
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireInstalled(w, r) {
		return
	}
	data := s.baseData(r)
	data["Nav"] = "settings"
	data["Section"] = "profile"
	tr := s.tr(r)
	if k := takeFlash(w, r, "profile", "ok"); k != "" {
		data["FlashOK"] = tr(k)
	}
	if k := takeFlash(w, r, "profile", "err"); k != "" {
		data["FlashErr"] = tr(k)
	}
	s.profileData(r, data)
	s.render(w, r, "profile", data)
}

// profileData наполняет data полями профиля (перечитывая пользователя из БД).
func (s *Server) profileData(r *http.Request, data map[string]any) {
	u := currentUser(r)
	if u != nil {
		if st := s.getStore(); st != nil {
			if fresh, err := st.GetUserByID(r.Context(), u.ID); err == nil {
				u = fresh
				data["User"] = u
			}
		}
	}
	if u == nil {
		return
	}
	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	data["DisplayName"] = strings.TrimSpace(u.DisplayName)
	data["Email"] = email
	data["GreetName"] = u.NameForGreeting()
	data["AllowAdult"] = u.AllowAdult
	data["PrefLang"] = u.PrefLang
	data["PrefTheme"] = u.PrefTheme
	data["ReadFont"] = u.ReadFont
	fontSize := u.ReadFontSize
	if fontSize == 0 {
		fontSize = defaultFontSize
	}
	data["ReadFontSize"] = fontSize
	data["HasAvatar"] = u.Avatar != ""
	data["AvatarURL"] = avatarURL(u)
	data["Initial"] = greetingInitial(u.NameForGreeting())
}

// handleProfileSave сохраняет имя, email и предпочтения (POST /settings/profile).
func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	upd := store.ProfileUpdate{UserID: u.ID}

	name := strings.TrimSpace(r.PostFormValue("display_name"))
	if rs := []rune(name); len(rs) > 100 {
		name = string(rs[:100])
	}
	upd.DisplayName = name

	// Email меняем только если поле присутствовало в форме.
	if _, ok := r.PostForm["email"]; ok {
		em := strings.TrimSpace(r.PostFormValue("email"))
		if em != "" && !looksLikeEmail(em) {
			s.flash(w, r, "profile", "err", "profile.err_email")
			s.redirect(w, r, "/settings/profile")
			return
		}
		emc := em
		upd.Email = &emc
	}

	lang := r.PostFormValue("pref_lang")
	if !validPrefLangs[lang] {
		lang = ""
	}
	theme := r.PostFormValue("pref_theme")
	if !validPrefThemes[theme] {
		theme = ""
	}
	font := r.PostFormValue("read_font")
	if !validReadFonts[font] {
		font = ""
	}
	size, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("read_font_size")))
	if size == 0 {
		size = defaultFontSize // пусто/0 — значение по умолчанию 100 %
	}
	if size < fontSizeMin {
		size = fontSizeMin
	}
	if size > fontSizeMax {
		size = fontSizeMax
	}
	upd.PrefLang, upd.PrefTheme, upd.ReadFont, upd.ReadFontSize = lang, theme, font, size

	if err := st.UpdateProfile(r.Context(), upd); err != nil {
		s.log.Warn("profile: update", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}

	// Мгновенно применяем выбранные язык/тему.
	if theme != "" {
		setThemeCookie(w, theme)
	}
	if lang != "" {
		setLangCookie(w, lang)
	}

	s.logEventUser(r.Context(), u, store.EventInfo, "profile.update", nil)
	s.flash(w, r, "profile", "ok", "profile.ok_saved")
	s.redirect(w, r, "/settings/profile")
}

// handlePasswordChange меняет пароль с проверкой текущего.
func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	oldPw := r.PostFormValue("old_password")
	newPw := r.PostFormValue("new_password")
	confirm := r.PostFormValue("confirm_password")

	if len([]rune(oldPw)) == 0 {
		s.flash(w, r, "profile", "err", "profile.err_pw_wrong")
		s.redirect(w, r, "/settings/profile")
		return
	}
	if len([]rune(newPw)) < 8 {
		s.flash(w, r, "profile", "err", "profile.err_pw_short")
		s.redirect(w, r, "/settings/profile")
		return
	}
	if newPw != confirm {
		s.flash(w, r, "profile", "err", "profile.err_pw_mismatch")
		s.redirect(w, r, "/settings/profile")
		return
	}
	fresh, err := st.GetUserByID(r.Context(), u.ID)
	if err != nil {
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	ok, err := auth.VerifyPassword(oldPw, fresh.PasswordHash)
	if err != nil || !ok {
		s.flash(w, r, "profile", "err", "profile.err_pw_wrong")
		s.redirect(w, r, "/settings/profile")
		return
	}
	hash, err := auth.HashPassword(newPw)
	if err != nil {
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	if err := st.SetUserPasswordHash(r.Context(), u.ID, hash); err != nil {
		s.log.Warn("profile: set password", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	s.logEventUser(r.Context(), u, store.EventWarn, "profile.password", nil)
	s.flash(w, r, "profile", "ok", "profile.ok_pw")
	s.redirect(w, r, "/settings/profile")
}

// handleAvatarUpload принимает изображение, обрезает в квадрат и сохраняет.
func (s *Server) handleAvatarUpload(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseMultipartForm(avatarMaxBytes); err != nil {
		s.flash(w, r, "profile", "err", "profile.err_avatar_big")
		s.redirect(w, r, "/settings/profile")
		return
	}

	// Удаление аватара.
	if r.FormValue("delete") == "1" {
		s.removeAvatar(u.ID)
		_ = st.SetUserAvatar(r.Context(), u.ID, "")
		s.flash(w, r, "profile", "ok", "profile.ok_avatar_removed")
		s.redirect(w, r, "/settings/profile")
		return
	}

	file, _, err := r.FormFile("avatar")
	if err != nil {
		s.flash(w, r, "profile", "err", "profile.err_avatar_none")
		s.redirect(w, r, "/settings/profile")
		return
	}
	defer file.Close()

	img, _, err := image.Decode(io.LimitReader(file, avatarMaxBytes))
	if err != nil {
		s.flash(w, r, "profile", "err", "profile.err_avatar_format")
		s.redirect(w, r, "/settings/profile")
		return
	}
	square := squareThumb(img, avatarMaxSide)

	dir, err := s.avatarDir()
	if err != nil {
		s.log.Warn("profile: avatar dir", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	name := avatarFilename(u.ID)
	dst := filepath.Join(dir, name)
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		s.log.Warn("profile: avatar create", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	encErr := jpeg.Encode(f, square, &jpeg.Options{Quality: avatarJPEGQual})
	f.Close()
	if encErr != nil {
		_ = os.Remove(tmp)
		s.log.Warn("profile: avatar encode", "err", encErr)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		s.log.Warn("profile: avatar rename", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	if err := st.SetUserAvatar(r.Context(), u.ID, name); err != nil {
		s.log.Warn("profile: set avatar", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	s.logEventUser(r.Context(), u, store.EventInfo, "profile.avatar", nil)
	s.flash(w, r, "profile", "ok", "profile.ok_avatar")
	s.redirect(w, r, "/settings/profile")
}

// handleAvatarServe отдаёт аватар (текущего или ?u=<id>).
func (s *Server) handleAvatarServe(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.NotFound(w, r)
		return
	}
	target := u
	if idStr := r.URL.Query().Get("u"); idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			if id != u.ID {
				if other, err := st.GetUserByID(r.Context(), id); err == nil {
					target = other
				}
			}
		}
	}
	if target.Avatar == "" {
		http.NotFound(w, r)
		return
	}
	dir, err := s.avatarDir()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(dir, filepath.Base(target.Avatar))
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

// handleLogoutOthers завершает все прочие сессии пользователя.
func (s *Server) handleLogoutOthers(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = c.Value
	}
	n, err := st.DeleteUserSessionsExcept(r.Context(), u.ID, keep)
	if err != nil {
		s.log.Warn("profile: logout others", "err", err)
		s.flash(w, r, "profile", "err", "profile.err_save")
		s.redirect(w, r, "/settings/profile")
		return
	}
	s.logEventUser(r.Context(), u, store.EventWarn, "profile.logout_others",
		map[string]string{"count": strconv.FormatInt(n, 10)})
	s.flash(w, r, "profile", "ok", "profile.ok_logout_others")
	s.redirect(w, r, "/settings/profile")
}

// removeAvatar удаляет файл аватара пользователя (если есть).
func (s *Server) removeAvatar(userID int64) {
	if dir, err := s.avatarDir(); err == nil {
		_ = os.Remove(filepath.Join(dir, avatarFilename(userID)))
	}
}

// squareThumb масштабирует и центрально обрезает изображение в квадрат max×max.
func squareThumb(src image.Image, max int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return image.NewRGBA(image.Rect(0, 0, max, max))
	}
	side := w
	if h < side {
		side = h
	}
	offX := b.Min.X + (w-side)/2
	offY := b.Min.Y + (h-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, max, max))
	scale := float64(side) / float64(max)
	for y := 0; y < max; y++ {
		sy := offY + int(float64(y)*scale)
		if sy >= b.Max.Y {
			sy = b.Max.Y - 1
		}
		for x := 0; x < max; x++ {
			sx := offX + int(float64(x)*scale)
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// looksLikeEmail — грубая валидация email.
func looksLikeEmail(s string) bool {
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	dot := strings.LastIndexByte(s[at+1:], '.')
	return dot > 0
}

// greetingInitial — первая буква имени (для заглушки аватара).
func greetingInitial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}
