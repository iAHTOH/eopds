package admin

import (
	"context"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Фоновое обслуживание БД: периодическая уборка просроченных данных.
//
// Задачи:
//   - email_tokens — удаление просроченных и уже использованных одноразовых
//     токенов (ссылки сброса пароля). Раньше PurgeEmailTokens вызывался только
//     вручную/из тестов, и таблица росла без ограничений.
//
// Задача запускается сразу после старта (чтобы не ждать часа на «грязной»
// базе) и затем раз в maintInterval. Работает в отдельной горутине и
// останавливается по отмене контекста (см. Server.StartMaintenance).
//
// Решение остановить процесс не зависит от задач: если задача упала с
// ошибкой, она пишется в лог, а следующая попытка — через интервал.

const (
	// maintInterval — период фонового обслуживания.
	maintInterval = time.Hour
	// maintInitialDelay — пауза перед первым прогоном после старта: даёт
	// сервису подняться и не конкурировать с миграциями/сканом.
	maintInitialDelay = 2 * time.Minute
)

// StartMaintenance запускает фоновое обслуживание БД и возвращает функцию
// остановки. Повторный вызов безопасен: предыдущая задача не отменяется, но
// на практике вызывается один раз из main после подключения БД.
func (s *Server) StartMaintenance(ctx context.Context) (stop func()) {
	s.LogMaintenanceStart()
	ctx, cancel := context.WithCancel(ctx)
	go s.maintenanceLoop(ctx)
	return cancel
}

// maintenanceLoop — цикл обслуживания.
func (s *Server) maintenanceLoop(ctx context.Context) {
	first := time.NewTimer(maintInitialDelay)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
		s.maintenanceRun(ctx)
	}

	tick := time.NewTicker(maintInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.maintenanceRun(ctx)
		}
	}
}

// maintenanceRun выполняет один проход обслуживания и возвращает число
// удалённых токенов почты (для тестов и лога).
func (s *Server) maintenanceRun(ctx context.Context) int64 {
	st := s.getStore()
	if st == nil {
		return 0
	}
	// Отдельный таймаут на проход: не зависаем на заблокированной БД.
	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	n, err := s.purgeEmailTokens(runCtx, st)
	if err != nil {
		s.log.Warn("обслуживание: очистка токенов почты", "err", err)
	}
	return n
}

// LogMaintenanceStart сообщает в лог, что фоновая задача обслуживания
// запущена. Вызывается один раз при старте (из StartMaintenance), чтобы
// администратор видел её в логе рядом с «HTTP-сервер запущен».
func (s *Server) LogMaintenanceStart() {
	s.log.Info("обслуживание БД запущено", "каждые", maintInterval.String(),
		"первый запуск через", maintInitialDelay.String())
}

// PurgeEmailTokens удаляет просроченные и использованные токены почты и,
// если что-то удалено, пишет сообщение системы mail.tokens_purged.
// Возвращает число удалённых строк.
func (s *Server) PurgeEmailTokens(ctx context.Context, st store.Store) error {
	_, err := s.purgeEmailTokens(ctx, st)
	return err
}

// purgeEmailTokens — общая часть: удаление + событие, возвращает число строк.
func (s *Server) purgeEmailTokens(ctx context.Context, st store.Store) (int64, error) {
	n, err := st.PurgeEmailTokens(ctx)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		s.logEvent(ctx, store.EventInfo, "mail.tokens_purged", map[string]string{
			"count": itoa64(n),
		})
	}
	return n, nil
}
