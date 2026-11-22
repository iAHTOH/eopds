# OPDS-каталог

Отдача библиотеки по протоколу **OPDS** — ядро проекта. Поддерживаются обе
версии протокола с одним набором данных и двумя тонкими сериализациями
(пакет `internal/opds`):

- **OPDS 1.2 (Atom XML)** — `application/atom+xml;profile=opds-catalog;kind=...`.
  Понимают все читалки (Moon+, FBReader, KOReader, Aldiko, PocketBook и т.д.).
- **OPDS 2.0 (JSON)** — `application/opds+json` (Readium-совместимые клиенты).

## Согласование формата

Один и тот же URL отдаёт 1.2 или 2.0 в зависимости от запроса:

1. явный `?format=json` → OPDS 2.0; `?format=atom` → OPDS 1.2;
2. заголовок `Accept: application/opds+json` (и без `atom+xml`) → 2.0;
3. иначе — **OPDS 1.2 (Atom)** по умолчанию (максимальная совместимость).

Ответ несёт соответствующий `Content-Type`.

## Публичный доступ

OPDS-каталоги (`/opds*`) **публичны** — читалки ходят без сессии.
Скачивание файлов (`/library/book/{id}/download`) по-прежнему требует входа:
acquisition-ссылки для гостя перенаправят на `/login`. Для доступа к файлам
настройте вход в читалке (логин/пароль пользователя библиотеки).

## Маршруты

| Метод | Путь | Тип feed | Назначение |
|---|---|---|---|
| GET | `/opds` | navigation | корень: «Новые», «Все книги», «Авторы», «Серии», «Жанры» |
| GET | `/opds/new` | acquisition | новые поступления (книги по `id` DESC) |
| GET | `/opds/books` | acquisition | все книги (по алфавиту); фильтры `?author=`, `?series=`, `?genre=` |
| GET | `/opds/authors` | navigation | авторы → книги автора (`?prefix=`, `?page=`) |
| GET | `/opds/series` | navigation | серии → книги серии |
| GET | `/opds/genres` | navigation | жанры → книги жанра |
| GET | `/opds/search?q=...` | acquisition | поиск по названию (также `?searchTerms=`) |
| GET | `/opds/search.xml` | — | OpenSearch-описание (`rel=search`) |

Пагинация: `?page=N` (50 записей на страницу), в feed — ссылки `rel=next` /
`rel=previous`.

## Запись книги (acquisition)

Каждая книга — запись с:

- `id` = `urn:eopds:book:<id>`, `title`, `updated`, `summary` (аннотация),
  авторы, категории (жанры, `term`/`label`);
- обложки: `rel="http://opds-spec.org/image"` и `.../image/thumbnail"`
  (`/library/book/{id}/cover`, `image/jpeg`);
- скачивание: `rel="http://opds-spec.org/acquisition"`:
  - исходный файл — `/library/book/{id}/download?fmt=<исходный>` с MIME по формату;
  - для FB2 — дополнительно `?fmt=fb2+zip` (`application/zip`).

MIME: FB2 → `application/x-fictionbook+xml`, EPUB → `application/epub+zip`,
PDF → `application/pdf`, TXT → `text/plain`, MD → `text/markdown`.

> Конвертация в EPUB/др. форматы ещё не подключена (см. roadmap):
> `?fmt=` для не-исходных форматов сейчас даёт `404`.

## Примеры

Корень в Atom:

```bash
curl -H 'Accept: application/atom+xml' http://SERVER:8080/opds
```

OPDS 2.0 (JSON):

```bash
curl 'http://SERVER:8080/opds/new?format=json'
```

Поиск:

```bash
curl 'http://SERVER:8080/opds/search?q=Конан&format=atom'
```

## Подключение в читалке

Введите адрес каталога `http://SERVER:8080/opds` в OPDS-клиенте
(Moon+ Reader, FBReader, KOReader, PocketBook…). Клиент сам откроет feed и
покажет книги с кнопкой «Скачать».

## Устройство кода

- `internal/opds` — структуры `Feed/Entry/Link` и сериализаторы:
  `Atom(w, feed)` (1.2) и `JSON(w, feed)` (2.0). Никакой работы с БД.
- `internal/admin/opds_handler.go` — хендлеры: собирают данные из `internal/store`
  (переиспользуют `LatestBooks`, `ListBooks`, `SearchBooks`, `ListAuthors`,
  `ListSeries`, `ListGenres`) и зовут `opds.Atom`/`opds.JSON`.
- Выбор формата — `opdsWantJSON(r)`.

Тесты: `internal/opds/opds_test.go` (сериализация/экранирование/JSON) и
`internal/admin/opds_handler_test.go` (согласование формата, MIME).
