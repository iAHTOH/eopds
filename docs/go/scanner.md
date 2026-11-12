# Архитектура Go-сканера (экспериментальный движок)

Документ описывает экспериментальный быстрый сканер коллекции, написанный на Go,
и его интеграцию в eopds наряду со штатным Python-сканером.

## Цели

- Ускорить сканирование больших коллекций за счёт параллельного обхода и парсинга.
- Не ломать существующий Python-пайплайн: запись в БД, модели, OPDS-выдача
  остаются в Python (Django ORM).
- Полный дата-паритет с Python-сканером (книги, авторы, серии, жанры).
- Безопасный откат на Python-движок при любой ошибке.

## Поток данных

```
opds_catalog/management/commands/eopds_scanner.py
        │  scan()  (выбор движка по config.EOPDS_SCANNER_ENGINE)
        ▼
   engine == 'go' ─────────────► opds_catalog/scan_engine_go.py
                                    │  запускает бинарник eopds-scanner
                                    │  и читает построчный JSON
                                    ▼
                          eopds-go/cmd/eopds (scan)
                              internal/walk   — параллельный обход (worker pool)
                              internal/fb2    — парсер FB2 (encoding/xml)
                              
                              internal/charset       — windows-1251 -> UTF-8
                                    │  эмитит JSON-строки:
                                    │   {"kind":"book", ...}
                                    │   {"kind":"stats", ...}
                                    ▼
   запись через opdsdb.addbook / addauthor / addseries / addgenre …
        (в transaction.atomic, тот же путь, что у Python-сканера)
```

Python-движок (по умолчанию) выполняет `opdsScanner.scan_all()` из
`opds_catalog/eopdscan.py` без изменений.

## Протокол Go-бинарника

Бинарник не трогает БД. Он обходит каталог `--root` и печатает в stdout
по одной JSON-строке на книгу (`kind:"book"`) и итоговую статистику
(`kind:"stats"`). Логи (`--level error|warn|info|debug`) идут в stderr.

Поля книги: `rel_path`, `filename`, `format`, `size`, `title`, `lang`,
`docdate`, `annotation`, `authors[]`, `genres[]`, `series{title,index}`,
`book_path`. Для zip-архива: `archive`, `member`, `catalog_type`.

Коды возврата: `0` — успех, `3` — неверные аргументы/корень,
`4` — внутренняя ошибка. Python-адаптер трактует ненулевой код как
фатальную ошибку и откатывается на Python-сканер.

## Ключевые оптимизации

1. **Ранний выход после `</description>`** — метаданные FB2 находятся только
   в `<description>`; парсер прекращает чтение сразу после него, не читая
   тело книги и base64-картинки. Это дало ускорение более чем в 100 раз
   на этапе парсинга.
2. **Потоковый charset** — windows-1251 транслируется в UTF-8 инкрементально,
   без загрузки всего файла в память (`internal/charset`).
3. **Пул воркеров** — параллельный обход и парсинг (`EOPDS_SCAN_WORKERS`).

## Дата-паритет с Python

- Авторы: `first-name` + `last-name` (как `book_tools/format/fb2.py`),
  затем адаптер переносит фамилию в начало (как `eopdscan.py`).
- Серия: **только первая** `<sequence>` из `title-info` (как Python).
- docdate: `document-info/date/@value`, иначе текст (как Python).
- Кодировки: windows-1251 декодируется (Python через lxml делает это сам).

Проверено на реальной коллекции (~2830 FB2): совпадение по авторам и сериям
с Python-извлечением 1:1, полный скан с записью в БД ~2x быстрее.

## Узкое место

После ускорения парсинга основное время занимает **однопоточная запись в БД**
через Django ORM (по одной книге с transaction). Дальнейшее ускорение требует
пакетной записи (`bulk_*`) — см. TODO.

## Сборка

Go-бинарник собирается в отдельном stage `Dockerfile` (`FROM golang:... AS goscanner`)
и копируется в `/usr/local/bin/eopds-scanner` (вне `/workspace`, чтобы не перекрывался
bind-mount'ом при dev-запуске). Версия Go: 1.23, внешних зависимостей нет.

## TODO

- Пакетная запись в БД (bulk inserts) для устранения узкого места.
- Поддержка кириллических кодировок, отличных от windows-1251 (koi8-r, cp866).
- Полноценные EPUB/MOBI метаданные (сейчас — только базовые).
- Типизированная обработка ошибок Go-процесса (сейчас — generic Exception).
- Инкрементальный скан (skip по размеру/дате) внутри Go-движка.
