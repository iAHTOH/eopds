package store

import (
	"context"
	"fmt"
	"strings"
)

// ProfileUpdate — редактируемые пользователем поля профиля.
// Email == nil — не менять; *Email == "" — очистить.
type ProfileUpdate struct {
	UserID       int64
	DisplayName  string
	Email        *string
	PrefLang     string
	PrefTheme    string
	ReadFont     string
	ReadFontSize int
}

// UpdateProfile обновляет имя, email и предпочтения пользователя.
func (s *DBStore) UpdateProfile(ctx context.Context, p ProfileUpdate) error {
	if p.UserID <= 0 {
		return fmt.Errorf("store: update profile: bad user id")
	}
	displayName := strings.TrimSpace(p.DisplayName)

	// Email меняем только если передан (nil → оставить прежний).
	if p.Email == nil {
		q := fmt.Sprintf(
			"UPDATE users SET display_name = %s, pref_lang = %s, pref_theme = %s, "+
				"read_font = %s, read_font_size = %s WHERE id = %s",
			s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6))
		_, err := s.db.ExecContext(ctx, q,
			displayName, p.PrefLang, p.PrefTheme, p.ReadFont, p.ReadFontSize, p.UserID)
		if err != nil {
			return fmt.Errorf("store: update profile: %w", err)
		}
		return nil
	}

	var emailArg any
	if e := strings.TrimSpace(*p.Email); e != "" {
		emailArg = e
	}
	q := fmt.Sprintf(
		"UPDATE users SET display_name = %s, email = %s, pref_lang = %s, pref_theme = %s, "+
			"read_font = %s, read_font_size = %s WHERE id = %s",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7))
	_, err := s.db.ExecContext(ctx, q,
		displayName, emailArg, p.PrefLang, p.PrefTheme, p.ReadFont, p.ReadFontSize, p.UserID)
	if err != nil {
		return fmt.Errorf("store: update profile: %w", err)
	}
	return nil
}

// SetUserAvatar устанавливает имя файла аватара (” — удалить).
func (s *DBStore) SetUserAvatar(ctx context.Context, id int64, avatar string) error {
	var arg any
	if a := strings.TrimSpace(avatar); a != "" {
		arg = a
	}
	q := fmt.Sprintf("UPDATE users SET avatar = %s WHERE id = %s", s.ph(1), s.ph(2))
	if _, err := s.db.ExecContext(ctx, q, arg, id); err != nil {
		return fmt.Errorf("store: set avatar: %w", err)
	}
	return nil
}

// DeleteUserSessionsExcept удаляет все сессии пользователя, кроме keepID.
func (s *DBStore) DeleteUserSessionsExcept(ctx context.Context, userID int64, keepID string) (int64, error) {
	q := fmt.Sprintf("DELETE FROM sessions WHERE user_id = %s", s.ph(1))
	args := []any{userID}
	if keepID != "" {
		q += fmt.Sprintf(" AND id <> %s", s.ph(2))
		args = append(args, keepID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: delete user sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
