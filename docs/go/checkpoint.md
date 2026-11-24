# Точка останова / шпаргалка (2026-11-24)

Срез состояния проекта на момент очистки чата. Задача файла — быстро вернуть
контекст и продолжить. Ветка — `dev`, версия — **0.63.0**.

## Состояние репозитория

- Последние коммиты (`dev`):
  - `73024a9` — `build(docker): alpine 3.20 → 3.24, сборщик golang 1.26 → 1.27.4`
  - `1dea4ea` — `feat(library): оценки, отзывы и популярные книги`
  - `a684d3d` — `feat(library): публичный просмотр для гостей, закрытый OPDS`
- Рабочее дерево чистое (кроме этой правки checkpoint.md, если ещё не закоммичена).
- **Не запушено**: `1dea4ea` и `73024a9` лежат локально в `dev` (`origin/dev`
  был на `c0495c2`). Проверить: `git log --oneline origin/dev..dev`;
  запушить: `git push origin dev`.
- `VERSION` = `0.63.0`. Схема БД — `schema_version` = **8**.

## Что реализовано (свежее, v0.62)

0. **Почта (SMTP), проверка адресов, сброс пароля по e-mail** (миграция `0008`,
   `schema_version` = 8). Док: [mail.md](mail.md).
   - Раздел «Настройки → Система → Почта (SMTP)» (`/settings?sec=mail`, admin):
     хост/порт/шифрование (`smtps`/`starttls`/`none`), логин/пароль
     (пусто — не менять, `-` — очистить), From/имя/Reply-To, включение отправки;
     «Проверить соединение» (TLS+вход без письма), «Тестовое письмо»,
     журнал отправок (последние 50) с очисткой.
   - `internal/mail` (go-mail): письма ru/en, валидация настроек (пароль только
     под TLS), выбор AUTH по предпочтению; `internal/emailcheck`: синтаксис + MX/A
     с кэшем — на недоставляемые адреса не шлём.
   - Сброс пароля: `/reset` → письмо (TTL 1 ч, одноразовый токен, в БД только
     SHA-256) → `/reset/confirm` (новый пароль, отзыв всех сессий); ответ на
     запрос всегда одинаковый.
   - События: `mail.sent/error/settings_saved/verify_ok/verify_failed/
     password_reset_requested/password_reset_done/log_cleared`.
   - Таблицы: `mail_smtp`, `mail_sent`, `email_tokens`.
   - Тесты: `internal/mail` (фейковый SMTP), `internal/emailcheck`,
     `store/mail_test.go`, `admin/mail_test.go` (полный цикл сброса, права).


1. **Оценки и отзывы к книгам** (миграция `0006`).
   - Рейтинг: 5 звёзд с половинками, `score 0..10` = 0..5 звёзд, одна оценка
     на пользователя (upsert), среднее/число — на лету.
   - Отзывы: добавление, удаление своего (модератор/админ — любой), без
     премодерации. Просмотр — всем, действия — авторизованным.
   - UI: блок рейтинга (клик по половине звезды) + отзывы на карточке книги.
   - Журнал: события `book.rate`, `book.comment`, `book.comment_delete`;
     ссылки актёр→профиль, книга→карточка (служебные args `_user_id`,
     `_book_id`; в `.log` — без ссылок).
   - Код: `store/reviews.go`, `admin/reviews.go`, `library_book.html`, CSS
     `library.css`/`theme-*.css` (`--star-fill`/`--star-empty`).
     Док: [library.md](library.md).

2. **Популярность и счётчик «Скачено»** (миграция `0007`).
   - `downloads`: одна строка на пару (книга, пользователь) — частичный
     уникальный индекс `downloads_book_user_uidx (book_id,user_id)
     WHERE user_id IS NOT NULL`; `AddDownload` идемпотентен
     (`ON CONFLICT DO NOTHING`). Повторы не раздувают таблицу.
   - **Популярность = уникальные читатели**: `COUNT(DISTINCT user_id)` +
     анонимные (`user_id IS NULL`). Повторами не накручивается.
   - Учитывает: скачивание, **открытие читалки** (`format='read'`,
     `handleBookRead` → `recordRead`), OPDS (через `/download`).
   - Карточка: «**Скачено: N**» (`lib.downloads_count`) над рейтингом.
   - Дашборд: «Популярные книги» (топ-3). Страница
     `/dashboard/popular?by=downloads|rating&page=&sort=desc|asc` — сортировка
     на уровне SQL.
   - Код: `store/popular.go`, `admin/popular.go`, `admin/server.go` (baseData).
     Док: [api.md](api.md), [library.md](library.md), [../db-schema.md](../db-schema.md).

3. **Docker**: рантайм `alpine:3.24`, сборщик `golang:1.27.4-alpine`
   (зафиксированы, не `latest`). Shell (busybox) сохранён намеренно — для
   отладки командами в контейнере. Образ ~44.6 МБ. См. `Dockerfile.go`.

## Более раннее (контекст вокруг v0.61)

- Проект Go-only (`eopds-go`), Python/Django удалён.
- Сканер: фоновый скан + прогресс, батч-импорт (≈5–7x), инкрементальный скан
  (`scan_files`, отпечатки size+mtime). [scanner.md](scanner.md).
- Онлайн-читалка FB2 (server-side): `/read`, `/html`, `/progress`. [reader.md](reader.md).
- Публичная библиотека для гостей; OPDS — только авторизованным.
- EPUB-экспорт `?fmt=epub|epub3|epub2|kepub`. [epub.md](epub.md).
- Профиль/настройки `/settings/profile`. [profile-settings.md](profile-settings.md).
- Журнал «Сообщения системы». [system-events.md](system-events.md).

## Решения от пользователя (не забывать)

- **Популярность = уникальные читатели** (вариант A): читать = тоже
  популярность; повторы не считаем.
- **Без раздувания таблицы**: одна строка на (книга, пользователь) — на уровне
  БД (уникальный индекс), не только в коде.
- **Бэкап**: счётчик/рейтинг/отзывы — обычные таблицы (`downloads`,
  `book_ratings`, `book_comments`); стандартный дамп сохраняет всё.
- **Docker**: shell оставляем (дебаг), версии фиксируем (не `latest`).
- **Подписи вкладок** популярного («По скачиванию»/«По рейтингу») оставлены
  как есть.
- Коммиты — рус/Conventional (`feat(...)`, `build(...)`, `chore(...)`).
  Документация — часть Definition of Done.

## Правила и грабли

- Новая функция/команда/API/конфиг → страница в `docs/` в том же коммите;
  индекс — [../README.md](../README.md).
- Версия — SemVer, единый источник `VERSION` (→ ldflags `internal/version`).
  Изменение схемы → инкремент `schema_version` + миграция в
  `eopds-go/internal/migrate/migrations/{pg,sqlite}/`.
- Перед коммитом Go: `gofmt -l`, `go build ./...`, `go test -count=1 ./...`.
- **Не делать массовые `DELETE` без фильтра** (был инцидент с `system_events`).
  Удалять только тестовые строки точечно (`WHERE book_id=… AND user_id=…`).
- Docker `build --no-cache` иногда падает на IPv6 к registry
  (`network is unreachable`) — помогает явный `docker pull <tag>`.

## Быстрый старт

```bash
docker compose -f docker-compose.go.yml up -d
docker compose -f docker-compose.go.yml build --no-cache eopds-go && \
  docker compose -f docker-compose.go.yml up -d eopds-go
docker compose -f docker-compose.go.yml exec eopds-go sh          # shell (дебаг)
docker compose -f docker-compose.go.yml exec db-go psql -U eopds -d eopds
```

Проверки:
- `curl -s http://127.0.0.1:8080/api/health` → `status: ok`, `schema_version: 8`;
- `curl -s http://127.0.0.1:8080/api/version` → `0.63.0`, `go: go1.27.4`.

Тестовые учётки (вход `POST /login`): `admin/password123` (root),
`moderator/moderator123`, `reader/reader123`.

Скан-каталог: `/books` в контейнере, из `/run/media/ahtoh/SSD2/e-book`.

## Что дальше

- Текущая задача пользователя — на момент очистки: разные доработки по ходу,
  единого тикета нет; «ещё много работы».
- Перед релизом: запушить `dev` (origin отстаёт на 2 коммита), релизные ноты.
- Кандидаты (по желанию): точная позиция чтения, кэш HTML читалки, эндпоинт
  картинок вместо data URI; телеграм-бот/публичный портал — [roadmap.md](../roadmap.md).

## Как начать новый чат

Одна фраза:

> «Продолжаем eopds. Прочитай `docs/go/checkpoint.md` и `docs/README.md`,
> кратко перескажи состояние и предложи следующий шаг. Задача: <…>»
