// Package emailcheck — проверка адресов электронной почты EOPDS.
//
// Проверка сознательно ограничена тем, что можно сделать надёжно и без
// «шпионских» приёмов:
//
//  1. Синтаксис по RFC 5322 (net/mail) + обязательная точка в домене.
//  2. Наличие домена — A/AAAA-запись, либо MX-запись (для получения почты
//     есть и «эффект неявного MX» — A-запись домена).
//
// SMTP-пробинг (RCPT TO) намеренно НЕ делается: он недостоверен (catch-all,
// greylisting), часто блокируется провайдерами и выглядит как разведка
// адресов. Единственное надёжное подтверждение адреса — переход по ссылке
// из письма (double opt-in), это отдельный сценарий.
//
// Результаты кэшируются в памяти: и положительные, и отрицательные, с TTL,
// чтобы не дёргать DNS на каждый запрос из интерфейса.
package emailcheck

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"sync"
	"time"
)

// Результаты проверки (коды для интерфейса).
const (
	StatusOK        = "ok"        // адрес пригоден к отправке
	StatusSyntax    = "syntax"    // неверный синтаксис / пустой
	StatusNoDomain  = "no_domain" // у домена нет A/AAAA/MX
	StatusDNSError  = "dns_error" // временная ошибка DNS (не приговор)
	StatusUnknown   = "unknown"   // проверка не выполнялась
	StatusNotNeeded = "skipped"   // проверка отключена в настройках
)

// Result — итог проверки адреса.
type Result struct {
	Address string
	Status  string
	Domain  string
	// Records — какие записи найдены: "mx" или "a".
	Records []string
	// CheckedAt — unix ms выполнения проверки.
	CheckedAt int64
	// Err — текст ошибки для dns_error.
	Err string
}

// Valid сообщает, что адрес пригоден к отправке.
func (r Result) Valid() bool { return r.Status == StatusOK }

// ReasonKey — ключ i18n с пояснением результата.
func (r Result) ReasonKey() string {
	switch r.Status {
	case StatusOK:
		return "mail.check.ok"
	case StatusSyntax:
		return "mail.check.syntax"
	case StatusNoDomain:
		return "mail.check.no_domain"
	case StatusDNSError:
		return "mail.check.dns_error"
	case StatusNotNeeded:
		return "mail.check.skipped"
	default:
		return "mail.check.unknown"
	}
}

// Resolver — интерфейс DNS-резолвера (для тестов).
type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// ResolverFunc — адаптер «функция → Resolver» (удобен для тестов и подмены).
// LookupMX всегда возвращает пустой список, решения принимает LookupHost.
type ResolverFunc func(ctx context.Context, host string) ([]string, error)

// LookupMX реализует Resolver.
func (f ResolverFunc) LookupMX(context.Context, string) ([]*net.MX, error) { return nil, nil }

// LookupHost реализует Resolver.
func (f ResolverFunc) LookupHost(ctx context.Context, host string) ([]string, error) {
	return f(ctx, host)
}

// Checker — проверка адресов с кэшем.
type Checker struct {
	// TTL — срок жизни записи кэша (по умолчанию 1 час).
	TTL time.Duration
	// Timeout — таймаут одного DNS-запроса (по умолчанию 5 с).
	Timeout time.Duration

	res   Resolver
	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	res Result
	at  time.Time
}

// NewChecker создаёт проверяющий с системным DNS.
func NewChecker() *Checker {
	return &Checker{
		TTL:     time.Hour,
		Timeout: 5 * time.Second,
		res:     net.DefaultResolver,
		mu:      sync.Mutex{},
		cache:   make(map[string]cached),
	}
}

// NewCheckerWith создаёт проверяющий с заданным резолвером (для тестов).
func NewCheckerWith(r Resolver) *Checker {
	c := NewChecker()
	c.res = r
	return c
}

// Check проверяет адрес: синтаксис и существование домена (MX или A/AAAA).
func (c *Checker) Check(ctx context.Context, address string) Result {
	addr := strings.TrimSpace(address)
	res := Result{Address: addr, Status: StatusUnknown}

	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed.Address != addr {
		res.Status = StatusSyntax
		res.CheckedAt = time.Now().UnixMilli()
		return res
	}
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 {
		res.Status = StatusSyntax
		res.CheckedAt = time.Now().UnixMilli()
		return res
	}
	domain := strings.ToLower(addr[at+1:])
	res.Domain = domain
	if !strings.Contains(domain, ".") {
		res.Status = StatusSyntax
		res.CheckedAt = time.Now().UnixMilli()
		return res
	}

	// Кэш: и положительный, и отрицательный результат живут TTL.
	key := domain
	ttl := c.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && time.Since(e.at) < ttl {
		cachedRes := e.res
		cachedRes.Address = addr
		c.mu.Unlock()
		return cachedRes
	}
	c.mu.Unlock()

	res = c.lookup(ctx, addr, domain)

	c.mu.Lock()
	// Переполнение кэша — простая очистка (адресов в интерфейсе немного).
	if len(c.cache) > 4096 {
		c.cache = make(map[string]cached)
	}
	c.cache[key] = cached{res: withoutAddress(res), at: time.Now()}
	c.mu.Unlock()
	return res
}

// lookup выполняет DNS-запросы для домена.
func (c *Checker) lookup(ctx context.Context, addr, domain string) Result {
	res := Result{Address: addr, Domain: domain, CheckedAt: time.Now().UnixMilli()}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var dnsErr error
	if c.res != nil {
		if mxs, err := c.res.LookupMX(qctx, domain); err == nil && len(mxs) > 0 {
			res.Status = StatusOK
			res.Records = []string{"mx"}
			return res
		} else if err != nil {
			dnsErr = err
		}
		if hosts, err := c.res.LookupHost(qctx, domain); err == nil && len(hosts) > 0 {
			res.Status = StatusOK
			res.Records = []string{"a"}
			return res
		} else if err != nil && dnsErr == nil {
			dnsErr = err
		}
	}

	// Домен не имеет ни MX, ни A/AAAA. Различаем «нет такого домена» и сбой DNS.
	if dnsErr == nil || isNotFound(dnsErr) {
		res.Status = StatusNoDomain
		return res
	}
	res.Status = StatusDNSError
	res.Err = dnsErr.Error()
	return res
}

// isNotFound сообщает, что ошибка DNS означает «запись не найдена».
func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	return false
}

// withoutAddress копирует результат без адреса (в кэш кладём по домену).
func withoutAddress(r Result) Result {
	r.Address = ""
	return r
}

// ValidSyntax проверяет только синтаксис (без DNS) — для быстрых проверок.
func ValidSyntax(address string) bool {
	addr := strings.TrimSpace(address)
	parsed, err := mail.ParseAddress(addr)
	if err != nil || parsed.Address != addr {
		return false
	}
	at := strings.LastIndexByte(addr, '@')
	return at > 0 && strings.Contains(addr[at+1:], ".")
}
