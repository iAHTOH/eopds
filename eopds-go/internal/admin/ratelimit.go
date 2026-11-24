package admin

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter — простой счётчик попыток в скользящем окне.
//
// Используется для защиты публичных форм (сброс пароля) от генерации писем:
// ограничивает число запросов по одному ключу (адрес e-mail или IP).
// Хранилище — в памяти процесса; при рестарте счётчики обнуляются
// (это допустимо: цель — сдерживать спам, а не вести аудит).
//
// Реализация без горутин и таймеров: устаревшие записи вычищаются лениво
// при обращении, полная уборка — при переполнении map (см. sweepLocked).
type rateLimiter struct {
	mu      sync.Mutex
	limit   int           // допустимое число событий в окне
	window  time.Duration // длина окна
	entries map[string][]time.Time
	lastGC  time.Time
}

// newRateLimiter создаёт лимитер: не более limit событий за window.
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string][]time.Time),
		lastGC:  time.Now(),
	}
}

// Allow фиксирует попытку по ключу и сообщает, разрешена ли она.
// Возвращает также время до освобождения слота (для заголовка Retry-After),
// если попытка отклонена.
func (l *rateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if l == nil || l.limit <= 0 {
		return true, 0
	}
	key = strings.ToLower(strings.TrimSpace(key))
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	// Отбрасываем события старше окна.
	times := l.entries[key]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.entries[key] = kept
		// Освобождение — когда самое старое событие выйдет из окна.
		return false, kept[0].Add(l.window).Sub(now)
	}
	l.entries[key] = append(kept, now)
	return true, 0
}

// Reset сбрасывает счётчики (используется в тестах).
func (l *rateLimiter) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.entries = make(map[string][]time.Time)
	l.lastGC = time.Now()
	l.mu.Unlock()
}

// sweepLocked удаляет пустые записи; раз в 10 окон — полная уборка.
func (l *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastGC) < 10*l.window {
		return
	}
	cutoff := now.Add(-l.window)
	removed := 0
	for k, times := range l.entries {
		if len(times) == 0 || !times[len(times)-1].After(cutoff) {
			delete(l.entries, k)
			removed++
		}
	}
	_ = removed
	l.lastGC = now
}

// clientIP возвращает адрес клиента с учётом обратного прокси.
//
// X-Forwarded-For/X-Real-IP доверяем: портал по умолчанию работает за
// прокси, а лимит — лишь вспомогательный барьер. Берём последний адрес
// в X-Forwarded-For (его дописывает ближайший прокси), затем X-Real-IP.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.LastIndexByte(xff, ','); i >= 0 {
			xff = xff[i+1:]
		}
		if v := strings.TrimSpace(xff); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// retryAfterHeader — значение заголовка Retry-After в секундах (минимум 1).
func retryAfterHeader(d time.Duration) string {
	sec := int((d + time.Second - 1) / time.Second)
	if sec < 1 {
		sec = 1
	}
	return itoa(sec)
}
