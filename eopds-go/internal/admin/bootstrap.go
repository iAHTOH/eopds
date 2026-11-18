package admin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/iAHTOH/eopds/eopds-go/internal/auth"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// ApplyConfigOnStart синхронизирует содержимое config.json с БД при старте:
//   - корневой администратор (admin) — создаётся, если root ещё нет; если
//     задан иной password_hash, он обновляется;
//   - каталоги сканирования (scan_paths) — записываются в настройки БД,
//     если в конфиге они заданы.
//
// Ошибки не фатальны: сервер всё равно поднимается (мастер установки/UI
// покажет проблему). Возвращает первую ошибку для лога.
func (s *Server) ApplyConfigOnStart(ctx context.Context) error {
	st := s.getStore()
	if st == nil {
		return nil
	}
	var firstErr error
	setErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 1) Корневой администратор.
	setErr(s.applyRootAdmin(ctx, st))

	// 2) Каталоги сканирования из конфига.
	if len(s.cfg.ScanPaths) > 0 {
		if enc, err := json.Marshal(s.cfg.ScanPaths); err == nil {
			if err := st.SetSetting(ctx, scanPathsKey, string(enc), 0); err != nil {
				setErr(err)
			} else {
				s.log.Info("каталоги сканирования применены из конфига", "count", len(s.cfg.ScanPaths))
			}
		}
	}
	return firstErr
}

// applyRootAdmin создаёт/обновляет root-администратора из конфига.
func (s *Server) applyRootAdmin(ctx context.Context, st store.Store) error {
	a := s.cfg.Admin
	if a.Username == "" {
		return nil
	}

	installed, err := st.IsInstalled(ctx)
	if err != nil {
		return err
	}

	// Хеш: из конфига (password_hash) либо хешируем plain password (если задан).
	wantHash := a.PasswordHash
	if wantHash == "" && a.Password != "" {
		h, err := auth.HashPassword(a.Password)
		if err != nil {
			return err
		}
		wantHash = h
	}
	if wantHash == "" {
		return nil // нет данных для создания/обновления
	}

	if !installed {
		if _, err := st.CreateRootAdmin(ctx, a.Username, wantHash, a.Email); err != nil {
			if errors.Is(err, store.ErrRootExists) {
				return nil
			}
			return err
		}
		s.log.Info("root-администратор создан из конфига", "user", a.Username)
		return nil
	}

	// Root уже есть: при необходимости обновляем хеш пароля.
	u, err := st.GetUserByUsername(ctx, a.Username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("root-администратор из конфига не найден в БД (иной логин)", "user", a.Username)
			return nil
		}
		return err
	}
	if u.PasswordHash != wantHash {
		if err := st.SetUserPasswordHash(ctx, u.ID, wantHash); err != nil {
			return err
		}
		s.log.Info("пароль root-администратора обновлён из конфига", "user", a.Username)
	}
	return nil
}
