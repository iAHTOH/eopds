package store

import (
	"context"
	"fmt"
	"time"
)

// Уровни сообщений системы.
const (
	EventInfo  = "info"
	EventWarn  = "warn"
	EventError = "error"
)

// SystemEvent — одно сообщение системы (событие для дашборда).
// Текст не хранится: category + Args (подстановки) выводятся на языке
// пользователя через i18n.
type SystemEvent struct {
	ID        int64
	TS        int64 // unix ms
	Level     string
	Category  string
	Actor     string
	ActorRole string
	Args      map[string]string
}

// AddSystemEvent вставляет событие и выполняет ротацию: оставляет не более
// maxRows самых новых (maxRows <= 0 — без ограничения).
func (s *DBStore) AddSystemEvent(ctx context.Context, e SystemEvent, maxRows int) error {
	if e.Level == "" {
		e.Level = EventInfo
	}
	if e.TS == 0 {
		e.TS = time.Now().UTC().UnixMilli()
	}
	args := "{}"
	if len(e.Args) > 0 {
		if enc, err := marshalArgs(e.Args); err == nil {
			args = enc
		}
	}

	q := fmt.Sprintf(
		"INSERT INTO system_events (ts, level, category, actor, actor_role, args) "+
			"VALUES (%s, %s, %s, %s, %s, %s)",
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6),
	)
	if _, err := s.db.ExecContext(ctx, q,
		e.TS, e.Level, e.Category, e.Actor, e.ActorRole, args); err != nil {
		return fmt.Errorf("store: add system event: %w", err)
	}

	if maxRows > 0 {
		if err := s.rotateSystemEvents(ctx, maxRows); err != nil {
			return err
		}
	}
	return nil
}

// rotateSystemEvents удаляет старые события, оставляя maxRows самых новых.
func (s *DBStore) rotateSystemEvents(ctx context.Context, maxRows int) error {
	// Порог: ts самого нового «лишнего» события. Оставляем maxRows записей
	// с наибольшими ts (при равенстве ts различаем по id).
	q := fmt.Sprintf(
		"DELETE FROM system_events WHERE id NOT IN ("+
			"SELECT id FROM system_events ORDER BY ts DESC, id DESC LIMIT %s)", s.ph(1))
	if _, err := s.db.ExecContext(ctx, q, maxRows); err != nil {
		return fmt.Errorf("store: rotate system events: %w", err)
	}
	return nil
}

// SystemEvents возвращает последние limit событий (новые сверху).
func (s *DBStore) SystemEvents(ctx context.Context, limit int) ([]SystemEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	q := fmt.Sprintf(
		"SELECT id, ts, level, category, actor, actor_role, args "+
			"FROM system_events ORDER BY ts DESC, id DESC LIMIT %s", s.ph(1))
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("store: system events: %w", err)
	}
	defer rows.Close()

	var out []SystemEvent
	for rows.Next() {
		var e SystemEvent
		var args string
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Category,
			&e.Actor, &e.ActorRole, &args); err != nil {
			return nil, fmt.Errorf("store: scan system event: %w", err)
		}
		e.Args = unmarshalArgs(args)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountSystemEvents — число хранимых событий.
func (s *DBStore) CountSystemEvents(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM system_events").Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count system events: %w", err)
	}
	return n, nil
}

// ClearSystemEvents очищает журнал сообщений системы.
func (s *DBStore) ClearSystemEvents(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM system_events"); err != nil {
		return fmt.Errorf("store: clear system events: %w", err)
	}
	return nil
}
