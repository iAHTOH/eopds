package admin

import "github.com/iAHTOH/eopds/eopds-go/internal/store"

// NavItem — пункт левого меню раздела «Настройки».
type NavItem struct {
	Sec   string // значение ?sec=
	Label string // ключ i18n подписи
}

// NavGroup — группа пунктов меню с заголовком.
type NavGroup struct {
	Title string // ключ i18n заголовка группы
	Items []NavItem
}

// rank — числовой вес роли (больше — выше).
func rank(role string, isRoot bool) int {
	if isRoot || role == "admin" {
		return 3
	}
	if role == "moderator" {
		return 2
	}
	return 1
}

// settingsNav возвращает сгруппированное меню «Настроек» с учётом роли.
// Роль определяет, какие группы/пункты доступны:
//   - reader     — раздел недоступен (пусто);
//   - moderator  — Пользователи, Заявки;
//   - admin      — всё, включая библиотеку, систему и обновления (в конце).
func settingsNav(u *store.User) []NavGroup {
	if u == nil {
		return nil
	}
	r := rank(u.Role, u.IsRoot)

	var groups []NavGroup

	// Пользователи (модератор и админ).
	var users []NavItem
	if r >= 2 {
		users = append(users, NavItem{Sec: "users", Label: "settings.nav_users"})
	}
	if r >= 2 {
		users = append(users, NavItem{Sec: "requests", Label: "settings.nav_requests"})
	}
	if r >= 3 {
		users = append(users, NavItem{Sec: "invites", Label: "settings.nav_invites"})
	}
	if len(users) > 0 {
		groups = append(groups, NavGroup{Title: "settings.group_users", Items: users})
	}

	// Библиотека (только админ): сканирование с каталогами и опасной зоной.
	if r >= 3 {
		groups = append(groups, NavGroup{
			Title: "settings.group_library",
			Items: []NavItem{{Sec: "scan", Label: "settings.nav_scan"}},
		})
	}

	// Система (только админ).
	if r >= 3 {
		groups = append(groups, NavGroup{
			Title: "settings.group_system",
			Items: []NavItem{
				{Sec: "general", Label: "settings.nav_general"},
				{Sec: "mail", Label: "settings.nav_mail"},
				{Sec: "log", Label: "settings.nav_log"},
			},
		})
	}

	// Обслуживание (только админ) — в самом конце.
	if r >= 3 {
		groups = append(groups, NavGroup{
			Title: "settings.group_maint",
			Items: []NavItem{{Sec: "updates", Label: "settings.nav_updates"}},
		})
	}

	return groups
}
