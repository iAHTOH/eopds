package store

import "database/sql"

// rowScanner — общий интерфейс *sql.Row и *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// userSelectCols — список колонок users для SELECT (порядок совпадает с scanUser).
// prefix — квалификатор таблицы ("" или "u.") для JOIN-запросов.
func (s *DBStore) userSelectCols(prefix ...string) string {
	p := ""
	if len(prefix) > 0 {
		p = prefix[0]
	}
	col := func(c string) string { return p + c }
	return col("id") + ", " + col("username") + ", " + col("display_name") + ", " +
		col("email") + ", " + col("password_hash") + ", " + col("role") + ", " +
		col("is_root") + ", " + col("is_active") + ", " + col("avatar") + ", " +
		col("allow_adult") + ", " + col("pref_lang") + ", " + col("pref_theme") + ", " +
		col("read_font") + ", " + col("read_font_size") + ", " + s.createdAtExpr(p+"created_at")
}

// scanUser разбирает строку users (в порядке userSelectCols) в *User.
func scanUser(r rowScanner) (*User, error) {
	u := &User{}
	var email, avatar sql.NullString
	var isRoot, isActive, allowAdult any
	err := r.Scan(
		&u.ID, &u.Username, &u.DisplayName, &email, &u.PasswordHash, &u.Role,
		&isRoot, &isActive, &avatar, &allowAdult,
		&u.PrefLang, &u.PrefTheme, &u.ReadFont, &u.ReadFontSize, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.IsRoot = asBool(isRoot)
	u.IsActive = asBool(isActive)
	u.AllowAdult = asBool(allowAdult)
	if email.Valid {
		u.Email = &email.String
	}
	if avatar.Valid {
		u.Avatar = avatar.String
	}
	return u, nil
}
