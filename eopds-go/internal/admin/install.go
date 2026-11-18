package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/config"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// installData — данные формы установки.
type installData struct {
	Error string

	// Поля формы.
	DBType     string
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBPath     string

	Username string
	Email    string
}

// installView собирает данные для шаблона установки.
func (s *Server) installView(r *http.Request, d installData) map[string]any {
	data := s.baseData(r)
	data["Title"] = "Установка"
	data["Error"] = d.Error
	data["DBType"] = d.DBType
	data["DBHost"] = d.DBHost
	data["DBPort"] = d.DBPort
	data["DBName"] = d.DBName
	data["DBUser"] = d.DBUser
	data["DBPassword"] = d.DBPassword
	data["DBPath"] = d.DBPath
	data["Username"] = d.Username
	data["Email"] = d.Email
	return data
}

// handleInstallForm показывает мастер установки.
func (s *Server) handleInstallForm(w http.ResponseWriter, r *http.Request) {
	if s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	d := installData{
		DBType: string(s.cfg.DBType),
		DBHost: s.cfg.DBHost,
		DBPort: itoa(s.cfg.DBPort),
		DBName: s.cfg.DBName,
		DBUser: s.cfg.DBUser,
		DBPath: defaultSQLitePath(s.cfg),
	}
	s.render(w, r, "install", s.installView(r, d))
}

// handleInstallSubmit обрабатывает отправку мастера установки.
func (s *Server) handleInstallSubmit(w http.ResponseWriter, r *http.Request) {
	if s.isInstalled(r.Context()) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	d := installData{
		DBType:     strings.TrimSpace(r.PostFormValue("db_type")),
		DBHost:     strings.TrimSpace(r.PostFormValue("db_host")),
		DBPort:     strings.TrimSpace(r.PostFormValue("db_port")),
		DBName:     strings.TrimSpace(r.PostFormValue("db_name")),
		DBUser:     strings.TrimSpace(r.PostFormValue("db_user")),
		DBPassword: r.PostFormValue("db_password"),
		DBPath:     strings.TrimSpace(r.PostFormValue("db_path")),
		Username:   strings.TrimSpace(r.PostFormValue("username")),
		Email:      strings.TrimSpace(r.PostFormValue("email")),
	}
	password := r.PostFormValue("password")
	password2 := r.PostFormValue("password2")

	fail := func(msg string) {
		d.Error = msg
		s.render(w, r, "install", s.installView(r, d))
	}

	tr := s.i18n.Translator(resolveLang(r))
	if k := validateInstall(password, password2, d.Username); k != "" {
		fail(tr(k))
		return
	}

	// Собираем конфигурацию БД из формы.
	dbCfg := s.cfg
	dbCfg.DBType = config.DBType(d.DBType)
	dbCfg.DBHost = d.DBHost
	dbCfg.DBPort = atoi(d.DBPort, 5432)
	dbCfg.DBName = d.DBName
	dbCfg.DBUser = d.DBUser
	dbCfg.DBPassword = d.DBPassword
	dbCfg.DBPath = d.DBPath

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	st, err := store.Open(ctx, dbCfg, loggerAdapter{s.log})
	if err != nil {
		s.log.Error("install: подключение к БД", "err", err)
		fail(tr("install.err.db_connect"))
		return
	}

	if installed, err := st.IsInstalled(ctx); err != nil {
		_ = st.Close()
		s.log.Error("install: проверка установки", "err", err)
		fail(tr("install.err.db_read"))
		return
	} else if installed {
		_ = st.Close()
		fail(tr("install.err.already"))
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		_ = st.Close()
		s.log.Error("install: хэш пароля", "err", err)
		fail(tr("install.err.hash"))
		return
	}

	if _, err := st.CreateRootAdmin(ctx, d.Username, hash, d.Email); err != nil {
		_ = st.Close()
		if errors.Is(err, store.ErrRootExists) {
			fail(tr("install.err.already"))
			return
		}
		s.log.Error("install: создание админа", "err", err)
		fail(tr("install.err.create"))
		return
	}

	// Сохраняем базовые настройки.
	_ = st.SetSetting(ctx, "installed_at", time.Now().UTC().Format(time.RFC3339), 0)
	_ = st.SetSetting(ctx, "db_type", d.DBType, 0)

	// Сохраняем единый конфиг в /config/config.json: БД + root-админ.
	dbCfg.Admin = config.AdminConfig{
		Username:     d.Username,
		Email:        d.Email,
		PasswordHash: hash,
	}
	if err := config.SaveConfigFile(s.cfg.ConfigDir, dbCfg); err != nil {
		s.log.Warn("install: не удалось сохранить config.json", "err", err)
		// Не фатально: БД уже работает в памяти процесса.
	}

	// Делаем хранилище активным и обновляем конфигурацию сервера.
	s.mu.Lock()
	s.store = st
	s.cfg = dbCfg
	s.mu.Unlock()

	s.log.Info("установка завершена", "root", d.Username, "db", d.DBType)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// validateInstall проверяет поля формы; возвращает ключ ошибки или "".
func validateInstall(password, password2, username string) string {
	if len(username) < 3 {
		return "install.err.username"
	}
	if len(password) < 8 {
		return "install.err.password"
	}
	if password != password2 {
		return "install.err.password_mismatch"
	}
	return ""
}
