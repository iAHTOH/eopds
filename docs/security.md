# Безопасность: CSRF-защита форм

Все изменяющие состояние запросы портала защищены от **CSRF** (Cross-Site
Request Forgery). Реализация — `eopds-go/internal/admin/csrf.go`.

## Атака, от которой защищаемся

Сессия передаётся cookie `eopds_session` (`SameSite=Lax`). `Lax` закрывает
межсайтовые `POST` из формы на чужом сайте, но **не покрывает** все сценарии
(старые браузеры, `Lax+POST`-послабление в Chrome при свежей cookie, отправка
через `<a>`-навигацию с параметрами, прокси-подмена). Поэтому на изменяющих
запросах проверяется ещё и CSRF-токен — так форма, открытая на `evil.example`,
не сможет отправить запрос от имени пользователя.

## Схема: double-submit cookie + привязка к пользователю

1. При рендере страницы сервер генерирует случайный токен (32 байта,
   base64url) и выставляет cookie `eopds_csrf`.
2. Тот же токен попадает в HTML: `{{csrfField}}` — скрытое поле `_csrf`,
   `{{csrfToken}}` — строка для JS (`fetch`, `sendBeacon`).
3. На `POST` значение из формы (или заголовка `X-CSRF-Token`) сравнивается
   с токеном из cookie за постоянное время.
4. Если пользователь аутентифицирован, токен привязан к его `id`: в cookie
   хранится «пломба» — токен + усечённый SHA-256 от `csrf|<token>|<user_id>`.
   Поэтому токен, выданный одному пользователю, не действует в сессии другого.

### Параметры cookie

| Параметр | Значение | Зачем |
|---|---|---|
| `Name` | `eopds_csrf` | — |
| `HttpOnly` | да | JS не читает токен из cookie: он берётся со страницы |
| `SameSite` | `Lax` | вторая линия обороны вместе с сессионной cookie |
| `Secure` | при HTTPS | определяется по `TLS` или `X-Forwarded-Proto` |
| `Path` | `/` | — |
| `MaxAge` | сессионная | живёт, пока открыт браузер |

## Где проверяется

Проверка встроена в обёртки аутентификации (`requireAuth`, `optionalAuth`,
`requireAuthOPDS`), а не в отдельные обработчики — **новый маршрут защищён по
умолчанию**, забыть нельзя.

| Метод | Проверка |
|---|---|
| `GET`, `HEAD`, `OPTIONS` | нет (не меняют состояние) |
| `POST`, `PUT`, `PATCH`, `DELETE` | токен обязателен → иначе `403` |

### Исключения

- **HTTP Basic** (`Authorization: Basic …`) — OPDS-клиенты, `curl`, скрипты.
  Браузер не подставляет Basic-заголовок автоматически, поэтому CSRF-атака
  им невозможна, а токен взять негде.
- **`POST /install`** — мастер установки: пользователей ещё нет.
- Формы без обязательного входа (`/login`, `/reset`, `/reset/confirm`) —
  обёрнуты `publicForm`: токен проверяется, но без привязки к пользователю.

## Ответ при отказе

```
HTTP/1.1 403 Forbidden
Content-Type: text/plain; charset=utf-8

CSRF token missing or invalid. Reload the page and try again.
```

В лог сервиса пишется предупреждение:

```
level=WARN msg="csrf: токен не совпал" method=POST path=/reset remote=203.0.113.9
```

## Как добавить новую форму

```html
<form method="post" action="/settings/example">
  {{csrfField}}
  <!-- поля формы -->
  <button type="submit">Сохранить</button>
</form>
```

Для запросов из JS:

```js
var CSRF = {{csrfToken}};
fetch('/library/book/' + id + '/progress', {
  method: 'POST',
  body: fd,                      // fd.append('_csrf', CSRF) — или заголовок ниже
  credentials: 'same-origin',
  headers: {'X-CSRF-Token': CSRF}
});
```

Правила:

1. Каждая `POST`-форма содержит `{{csrfField}}`.
2. Каждый изменяющий `fetch`/`XHR`/`sendBeacon` передаёт токен полем `_csrf`
   или заголовком `X-CSRF-Token`.
3. Шаблоны разбираются с заглушками `csrfField`/`csrfToken` (см. `New` в
   `server.go`) — тесты шаблонов используют `addCSRF` из `templates_test.go`.

## Тесты

`eopds-go/internal/admin/csrf_test.go`:

| Тест | Что проверяет |
|---|---|
| `TestCSRFMissingTokenRejected` | POST без токена → `403` |
| `TestCSRFValidTokenAccepted` | корректный токен в поле → `303` |
| `TestCSRFMismatchedTokenRejected` | чужой токен (cookie ≠ форма) → `403` |
| `TestCSRFHeaderAccepted` | заголовок `X-CSRF-Token` без поля |
| `TestCSRFBasicAuthExempt` | запросы с Basic не блокируются |
| `TestCSRFFieldRendered` | поле `_csrf` на странице и совпадает с cookie |
| `TestCSRFGetNotChecked` | `GET` не требует токена |

Помощники: `getCSRF`, `csrfFromResp`, `csrfCookieHTTP`, `doWithCookies`
(`ratelimit_test.go`).

## Ограничения и что осталось

- Cookie сессионная: после перезапуска сервиса старые токены недействительны,
  пользователю достаточно обновить страницу.
- Токен один на страницу; при двух открытых вкладках обе получают один
  токен от одного пользователя — это допустимо. Переключение на другого
  пользователя в том же браузере автоматически выдаёт новый токен.
- Статьи для продвинутых сценариев пока нет: `SameSite=Strict` для сессии
  (ломает переходы из писем), CSP-политика, `Origin`-проверка. См. roadmap.

## Ссылки

- [go/api.md](go/api.md) — HTTP API и общая таблица кодов ответов.
- [go/mail.md](go/mail.md) — сброс пароля по почте и лимит частоты.
- [roadmap.md](roadmap.md) — дальнейшие шаги по безопасности.
