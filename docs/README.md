# Документация Easy OPDS (eopds)

Это индекс всей документации проекта. Документация — часть Definition of Done:
любая новая функция, команда или API получают страницу здесь, в том же коммите.

## Как читать эту документацию

- **Пользователям** — раздел [Руководство пользователя](#руководство-пользователя).
- **Администраторам серверов** — раздел [Развёртывание](#развёртывание).
- **Разработчикам** — раздел [Разработка](#разработка).

## Структура

### Обзор
- [architecture.md](architecture.md) — общая архитектура системы (Go).
- [security.md](security.md) — безопасность: CSRF-защита форм (токены, cookie, исключения, как добавить форму).
- [versioning.md](versioning.md) — версионирование, схема БД, проверка обновлений.
- [db-schema.md](db-schema.md) — проектирование новой схемы БД для Go-ядра.
- [roadmap.md](roadmap.md) — план развития проекта.

### Развёртывание
- [deploy/docker.md](deploy/docker.md) — запуск в Docker (PostgreSQL и SQLite).
- [deploy/configuration.md](deploy/configuration.md) — все параметры конфигурации (ENV и config.json).

### Разработка
- [architecture.md](architecture.md) — как устроен код.
- [go/README.md](go/README.md) — Go-подсистема `eopds-go` (сканер + админка).
- [go/install.md](go/install.md) — мастер первичной установки (root-админ, БД).
- [go/i18n-themes.md](go/i18n-themes.md) — темы оформления и локализация (ru/en).
- [go/api.md](go/api.md) — HTTP API Go-сервиса (версия, здоровье, обновления, форум).
- [go/library.md](go/library.md) — веб-библиотека Go (`/library/*`, алфавитный указатель, каркас страниц).
- [go/reader.md](go/reader.md) — онлайн-читалка FB2 (`/library/book/{id}/read`, `/html`, `/progress`).
- [go/opds.md](go/opds.md) — OPDS-каталог (1.2 Atom и 2.0 JSON), маршруты `/opds*`, acquisition/search.
- [go/epub.md](go/epub.md) — конвертер FB2→EPUB (2.0/3.0/kepub), `?fmt=` в download, структура EPUB.
- [go/mail.md](go/mail.md) — почта (SMTP): раздел «Почта (SMTP)», проверка адресов (MX), журнал отправок, сброс пароля по e-mail.
- [go/system-events.md](go/system-events.md) — «Сообщения системы» на дашборде: журнал событий, категории, ротация, скачивание `.log`.
- [go/profile-settings.md](go/profile-settings.md) — профиль пользователя (`/settings/profile`) и раздел «Настройки» (`/settings`): аватар, предпочтения, меню по ролям.
- [translating.md](translating.md) — как сделать перевод (для пулл-реквестов).
- [contributing.md](contributing.md) — как внести вклад в проект.

### Прочее
- [forum.md](forum.md) — интеграция с форумом eopds.ru (виджеты, источники).
- [go/checkpoint.md](go/checkpoint.md) — точка останова: где остановились и с чего продолжить.

## Соглашения

- Формат — Markdown (GFM).
- Код и имена — в обратных кавычках.
- Пути к файлам — относительно корня репозитория.
- Язык документации — русский (по умолчанию); переводы — в `docs/<lang>/`.
