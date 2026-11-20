# Схема БД для Go-ядра (проектирование)

> Статус: **утверждено, готовится DDL**. Окно для свободных изменений — пока
> не вышло стабильного релиза на новой схеме.

## Зачем новая схема

Go-ядро — фактически новый проект со своим веб-сервером и слоем данных.
Используем момент, чтобы спроектировать схему БД «правильно» с учётом:
- масштаба коллекции: **~1 000 000+ книг**;
- многопоточной/пакетной загрузки при сканировании;
- запросов OPDS-каталога (поиск/фасеты/пагинация);
- PostgreSQL (основной) и SQLite (файловая, для простых установок).

## Принципы

1. **Именование:** таблицы во множественном числе (`books`, `authors`),
   колонки `snake_case`, внешние ключи `<singular>_id`, промежуточные таблицы
   `book_authors`, `book_genres`, `book_series`.
2. **Типы:** `BIGINT` для идентификаторов и размеров; `TIMESTAMPTZ` для времени;
   `BOOLEAN` для флагов; `TEXT` для длинных строк (без искусственных VARCHAR).
3. **Переносимость PG/SQLite:** не использовать фичи, которых нет в SQLite
   (или изолировать их за адаптером `store`). Автогенерируемые идентификаторы —
   `BIGSERIAL`/`IDENTITY` в PG и `INTEGER PRIMARY KEY` в SQLite (через адаптер).
4. **Нормализация справочников:** авторы, серии, жанры, каталоги — отдельные
   таблицы; связь с книгой — через промежуточные.
5. **Производительность:** индексы под реальные запросы; денормализация только
   там, где нужна (например, `search_title` для сортировки/поиска).
6. **Версионирование схемы:** `schema_migrations` + `app_meta.schema_version`.

## Принятые решения

- **Чистая установка.** Совместимость со старой Django-схемой не нужна;
  **миграций со старой схемы нет**. Новый проект — новый каркас с нуля
  (старую БД не переносим). Автомиграции нужны только для будущих изменений
  уже новой схемы.
- **PostgreSQL — основная СУБД.** SQLite — упрощённый режим.
- **Автоматическая схема при установке (Easy).** Пользователь не запускает
  миграции вручную; миграции встроены в бинарник (`go:embed`) и применяются
  автоматически при старте (см. «Автомиграции при старте»).
- **Поиск на 1 млн:** префиксный поиск по индексу + опционально полнотекст
  (`pg_trgm` в PG / FTS5 в SQLite) для подстроки.
- **Дедупликация книг:** уникальный ключ `(path, filename)` (база для
  `INSERT ... ON CONFLICT` при bulk-загрузке).
- **ID:** `BIGINT` (`GENERATED ... AS IDENTITY` в PG, `INTEGER PK` в SQLite).
- **Счётчики:** денормализованный кэш в таблице `counters`.
- **Пользователи:** собственная таблица `users`. Роли `admin` (root, один) /
  `moderator` / `reader` + анонимный `guest`; права по ролям в коде.
  - Регистрация — **только по приглашению**; гость может оставить заявку
    (`registration_requests`).
  - Модератор приглашает **только читателей**; модераторов добавляет admin.
  - Удаление пользователя — **физическое** (данные не нужны).
- **Аудит:** таблица `audit_log` (кто что сделал).
- **Настройки** (SMTP, боты, порог новинок, режим обложек) — таблица `settings`,
  правит только админ.
- **Обложки:** таблица `book_covers` есть всегда; режим «извлекать онлайн /
  хранить на диске» — переключатель в админке с пояснениями. Внешние обложки
  (от провайдеров) сохраняются всегда.

## Роли и права

Фиксированные роли (права зашиты под роль в коде, не в БД):

| Действие | admin (root) | moderator | reader | guest (аноним) |
|---|---|---|---|---|
| Системные настройки | ✅ | ❌ | ❌ | ❌ |
| Приглашение пользователей (email) | ✅ | ✅ | ❌ | ❌ |
| Удаление пользователей | ✅ | ✅ | ❌ | ❌ |
| Обработка заявок на регистрацию | ✅ | ✅ | ❌ | ❌ |
| Жанры / аннотации / обложки (ручн. или провайдеры) | ✅ | ✅ | ❌ | ❌ |
| Ручное добавление/удаление книг | ✅ | ✅ | ❌ | ❌ |
| Просмотр логов/скан | ✅ | ✅ | ❌ | ❌ |
| Статистика библиотеки | ✅ | ✅ | ✅ | ✅ |
| Поиск / просмотр (обложка+аннотация) | ✅ | ✅ | ✅ | ✅ |
| Скачивание / чтение книг | ✅ | ✅ | ✅ | ❌ |
| Смена своего пароля | ✅ | ✅ | ✅ | — |
| Подписка на новинки / новости версий | ✅ | ✅ | ✅ | — |
| OPDS-выдача файлов | ✅ | ✅ | ✅ | ❌ |

- **Root-админ** — один, создаётся при инсталляции, неудаляем/непонижаем.
- Модераторов добавляет **только admin**; модератор приглашает **только
  читателей** (`invites.role='reader'`). Право проверяется в коде.
- **Гость** (аноним) видит главную как витрину: статистика + поиск + результаты
  (обложка+аннотация), **без скачивания/чтения**. OPDS-фиды гостю —
  навигационные/поисковые, но **без** acquisition-ссылок.
- **OPDS отдаётся авторизованным** пользователям любой роли.
- Заявка гостя на регистрацию → `registration_requests` → approve → `invite`.

## Автомиграции при старте

1. Проверяются `app_meta` и `schema_version`.
2. БД пустая → создаётся вся схема (миграции 1..N), пишется `schema_version`.
3. Версия устарела → применяются недостающие миграции по одной, каждая в своей
   транзакции; обновляется `app_meta.schema_version`.
4. Версия «из будущего» (БД новее бинарника) → отказ с понятной ошибкой.
5. Одновременный запуск процессов безопасен: блокировка (`pg_advisory_lock` в
   PG, `BEGIN EXCLUSIVE` в SQLite).

> Миграции — упорядоченные SQL-файлы через `go:embed`; специфика СУБД — в
> подкаталогах `migrations/pg/` и `migrations/sqlite/`.

### Применённые миграции

| Версия (`schema_version`) | Файл | Назначение |
|---------------------------|------|------------|
| 1 | `0001_baseline.sql` | Базовая схема ядра |
| 2 | `0002_book_lang_code.sql` | Бэкфилл `books.lang_code` по первому символу `search_title` (1/2/3/9) |

## Сканирование и отслеживание удалений

Ключ к корректному удалению — этапная пометка `avail` и снимок контейнеров
(size+mtime).

### `avail` (у книги)
- `0` — удалена; `1` — проверяется (не подтверждена текущим сканом);
- `2` — есть на диске (подтверждена текущим сканом).

### Цикл сканирования
1. Начало: `UPDATE books SET avail = 1 WHERE avail <> 0`.
2. Каждую найденную книгу помечаем `avail = 2`.
3. Конец: книги с `avail = 1` — не найдены (удалить: `avail = 0` или физически).

### Контейнеры: size + mtime (без хеша)
Для узлов-контейнеров (`catalogs.kind` = zip/inpx/inp) храним `size_bytes` и
`mtime`. Хеш не считаем — избыточно и медленно на 1 млн.
- Архив не изменился (size И mtime совпали) → skip: ставим его книгам `avail=2`,
  содержимое не перечитываем. Книгу из неизменного архива удалить физически
  нельзя — это корректно.
- Архив изменился (size или mtime разошлись) → перечитываем содержимое, что
  автоматически выявляет добавленные/удалённые внутри книги.

### Журнал
`scan_runs` — история сканирований (для дашборда и диагностики).

## Черновик таблиц

### `app_meta`
Метаданные приложения/схемы (одна строка).
| Колонка | Тип | Примечание |
|---|---|---|
| id | smallint PK | всегда 1 |
| schema_version | int | версия схемы |
| app_version | text | версия приложения при последней миграции |
| installed_at | timestamptz | |
| last_scan_at | timestamptz | время последнего сканирования |

### `schema_migrations`
| Колонка | Тип | Примечание |
|---|---|---|
| version | int PK | |
| applied_at | timestamptz | |
| description | text | |

### `counters`
Денормализованный кэш счётчиков (allbooks, allauthors, …).
| Колонка | Тип | Примечание |
|---|---|---|
| name | text PK | имя счётчика |
| value | bigint | |
| updated_at | timestamptz | |

### `scan_runs`
Журнал сканирований.
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| started_at | timestamptz | |
| finished_at | timestamptz NULL | |
| status | text | running/ok/error |
| books_added | bigint | |
| books_removed | bigint | |
| books_skipped | bigint | |
| error | text NULL | |

### `catalogs`
Дерево каталогов (папки, ZIP, INPX/INP).
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| parent_id | bigint FK→catalogs, NULL | корень — NULL |
| name | text | имя узла |
| path | text | полный путь |
| kind | smallint | 0=dir, 1=zip, 2=inpx, 3=inp |
| size_bytes | bigint | размер архива (skip) |
| mtime | timestamptz | время изменения файла (skip/изменения) |

Индексы: `(parent_id, name)`, уникальность `(path)`.

### `books`
Основная таблица (1 млн+ строк — критично к индексам).
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| catalog_id | bigint FK→catalogs | |
| filename | text | имя файла |
| path | text | путь (для файлов) / архив (для zip) |
| format | text | fb2, epub, … |
| size_bytes | bigint | |
| title | text | |
| search_title | text | верхний регистр для поиска/сортировки |
| original_title | text NULL | оригинальное название |
| annotation | text | |
| isbn | text NULL | |
| publisher | text NULL | издательство |
| publish_year | int NULL | год издания |
| docdate | text | как есть из метаданных |
| lang | text | код/имя языка |
| lang_code | smallint | 1=кириллица, 2=латиница, 3=цифры, 9=прочее |
| kind | smallint | 0=dir, 1=zip, 2=inpx, 3=inp (из catalog) |
| avail | smallint | 0=удалена, 1=проверяется, 2=есть |
| source | text NULL | откуда метаданные: extracted / провайдер |
| external_id | text NULL | id во внешнем источнике (провайдер) |
| added_at | timestamptz | |
| updated_at | timestamptz | |

Индексы:
- уникальность `(path, filename)` — дедупликация при сканировании;
- `(search_title)` — сортировка/префиксный поиск;
- `(lang_code, search_title)` — фильтр по языку + сортировка;
- `(avail)` — проход по «неподтверждённым» при сканировании.

> **Поиск на 1 млн:** префиксный (`LIKE 'x%'`) использует индекс. Для подстроки —
> `pg_trgm` (PG) / FTS5 (SQLite), вводится отдельной миграцией по необходимости
> и за интерфейсом `store`.

### `authors`
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| full_name | text | |
| search_full_name | text | верхний регистр |
| lang_code | smallint | |

Индексы: `(search_full_name)`; уникальность `(full_name)` для дедупликации.

### `book_authors`
| book_id | bigint FK | author_id | bigint FK |
|---|---|---|---|
PK `(book_id, author_id)`; индекс `(author_id, book_id)` для «книги автора».

### `genres`
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| code | text | fb2 genre code |
| section | text | |
| subsection | text | |

Индекс: уникальность `(code)`.

### `book_genres`
PK `(book_id, genre_id)`; индекс `(genre_id, book_id)`.

### `series`
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| title | text | |
| search_title | text | верхний регистр |
| lang_code | smallint | |

Индекс: уникальность `(title)`.

### `book_series`
| book_id | bigint FK | series_id | bigint FK | seq_no | int |
|---|---|---|---|---|---|
PK `(book_id, series_id)`; индекс `(series_id, seq_no)`.

### `users`
Собственная таблица пользователей (уход от Django `auth_user`).
Три роли: `admin` / `moderator` / `reader` (см. «Роли и права»).
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| username | text UNIQUE | |
| email | text NULL | необязательный; просим после регистрации |
| password_hash | text | bcrypt/argon2 |
| role | text | `admin` / `moderator` / `reader` (CHECK) |
| is_root | boolean | единственный root-админ (создан при инсталляции) |
| is_active | boolean | |
| notify_new_books | boolean | подписка на новинки (сводка) |
| notify_new_versions | boolean | новости о выходе новых версий |
| messenger_kind | text NULL | `telegram`/… (для новостей через бота) |
| messenger_id | text NULL | id в мессенджере |
| created_at | timestamptz | |

> **Root-админ — ровно один**, создаётся при инсталляции, удалить/понизить его
> нельзя. Гарантируется частичным уникальным индексом `(is_root) WHERE is_root`.
>
> **Удаление пользователя — физическое** (`DELETE`). Принимается, что данные
> пользователя больше не нужны. Связанные FK:
> `bookshelf`/`bookmarks`/`sessions` → `ON DELETE CASCADE`;
> `downloads.user_id`/`invites.created_by_id`/`audit_log.actor_id` →
> `ON DELETE SET NULL` (история/статистика сохраняются анонимно).

### `invites` (Issues #3 — приглашения на email)
| Колонка | Тип | Примечание |
|---|---|---|
| id | text PK (uuid) | |
| email | text | |
| role | text | кем станет приглашённый (обычно `reader`) |
| token | text UNIQUE | |
| created_at | timestamptz | |
| expires_at | timestamptz | |
| accepted_at | timestamptz NULL | |
| created_by_id | bigint FK→users NULL | |

### `registration_requests` (заявки гостей на регистрацию)
Гость оставляет запрос модератору/админу (через форму/бот). После одобрения
создаётся `invite`. Самостоятельной регистрации нет.
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| email | text | контакт заявителя |
| message | text NULL | комментарий |
| status | text | `new` / `approved` / `rejected` (CHECK) |
| created_at | timestamptz | |
| handled_at | timestamptz NULL | |
| handled_by_id | bigint FK→users NULL | кто обработал (`ON DELETE SET NULL`) |
Индекс `(status, created_at)` — очередь новых заявок.

### `audit_log` (журнал действий администрации)
Лёгкий аудит: кто что сделал (для разбора). `actor` — снимком + FK (SET NULL).
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| actor_id | bigint FK→users NULL | `ON DELETE SET NULL` |
| actor_name | text | снимок имени (переживёт удаление) |
| action | text | add/delete/edit/approve/… |
| target_type | text | book/user/genre/… |
| target_id | text NULL | id объекта (текстом — разные типы) |
| details | text NULL | краткое описание |
| created_at | timestamptz | |
Индекс `(created_at)`; `(target_type, target_id)`.

### `settings` (системные настройки)
Хранилище системных настроек (ключ-значение), редактируется **только админом** (SMTP, боты,
порог «новинок», режим обложек и т.п.).
| Колонка | Тип | Примечание |
|---|---|---|
| key | text PK | имя настройки |
| value | text | значение (JSON/строка) |
| updated_at | timestamptz | |
| updated_by_id | bigint FK→users NULL | `ON DELETE SET NULL` |

### `sessions`
| Колонка | Тип | Примечание |
|---|---|---|
| id | text PK | |
| user_id | bigint FK→users | |
| created_at | timestamptz | |
| expires_at | timestamptz | |
| data | text (json) | |

### `bookshelf` (Issues #2 — полка и прогресс)
| Колонка | Тип | Примечание |
|---|---|---|
| user_id | bigint FK→users | |
| book_id | bigint FK→books | |
| added_at | timestamptz | |
| last_read_at | timestamptz | |
PK `(user_id, book_id)`; индекс `(user_id, last_read_at)`.

### `bookmarks` (Issues #2 — закладки/позиция чтения)
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| user_id | bigint FK→users | |
| book_id | bigint FK→books | |
| position | text | CFI/позиция в книге |
| note | text NULL | |
| created_at | timestamptz | |
Индекс `(user_id, book_id)`.

### `annotations_cache` (Issues #1 — внешние аннотации)
| Колонка | Тип | Примечание |
|---|---|---|
| book_id | bigint FK→books, PK | |
| source | text | откуда взято |
| text | text | текст аннотации |
| fetched_at | timestamptz | |

### `book_covers` (обложки)
Метаданные обложек. **Сами изображения — файлами на диске** (каталог кеша),
в БД — только путь/размеры (blob в PG раздувает таблицы и бэкапы на 1 млн).

| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| book_id | bigint FK→books | |
| kind | smallint | 1=thumb, 2=full |
| source | text | extracted / provider:googlebooks / openlibrary / fantlab / … |
| format | text | jpeg/png/webp |
| width | int | |
| height | int | |
| size_bytes | bigint | |
| file_path | text | путь к файлу на диске (относительно COVERS_DIR) |
| fetched_at | timestamptz | |
PK/уникальность `(book_id, kind)`; индекс `(book_id)`.

> **Режим хранения выбирает пользователь в админке** (галочка с пояснениями):
> - **«Извлекать онлайн»** (по умолчанию): обложка берётся из файла книги при
>   запросе + HTTP-кеш. Таблица используется как «горячий» кеш, заполняется
>   лениво. Экономит место, подходит частным коллекциям.
> - **«Хранить на диске»**: обложки сохраняются в `COVERS_DIR` (таблица
>   `book_covers` — метаданные), отдаются сразу из файла. Быстрее для больших
>   библиотек, OPDS-фидов/поиска с превью, экономит CPU.
> - Обложки от **внешних провайдеров** сохраняются **всегда** (их нельзя
>   дёргать на каждый показ), независимо от режима.

### `downloads` (история скачиваний / «что читают»)
Лёгкая таблица событий для дашборда. На 1 млн книг — только факты скачиваний.
| Колонка | Тип | Примечание |
|---|---|---|
| id | bigint PK | |
| book_id | bigint FK→books | |
| user_id | bigint FK→users, NULL | NULL — анонимный OPDS |
| downloaded_at | timestamptz | |
| format | text | какой формат отдан (fb2/epub/…) |
Индекс `(downloaded_at)` (дашборд за период), `(book_id)` (популярность).

> Ротация: периодически чистить старые события (настройка срока хранения),
> чтобы не разрослась на 1 млн книг. Агрегаты для дашборда — в `counters`.

## Открытые вопросы

1. **Полнотекст:** включать `pg_trgm`/FTS5 сразу или отдельной миграцией позже?
   (Предложение: позже, по реальному объёму и профилю запросов.)
2. **`bookmarks.position`:** формат CFI (EPUB) — уточнить по мере читалки (#2).
3. **Счётчики:** обновлять в конце скана (без триггеров ради переносимости).

## Следующие шаги

- [x] Утвердить таблицы и типы.
- [x] Утвердить индексы и стратегию поиска (префикс + опц. FTS/trgm).
- [x] Утвердить стратегию удаления (`avail` + size/mtime контейнеров).
- [x] Добавить `isbn/publisher/publish_year/original_title/source/external_id`
      в `books`; таблицу `downloads`.
- [x] Роли (admin/moderator/reader/guest), root-админ, `registration_requests`,
      `audit_log`, `settings`, мессенджер в `users`.
- [ ] Написать DDL/миграции (PG и SQLite), встроенные в бинарник (+ автомиграции).
- [ ] Реализовать пакет `store` (интерфейс + pgx + sqlite).
- [ ] Пакетная загрузка (`ON CONFLICT` / `CopyFrom`) для сканера.
