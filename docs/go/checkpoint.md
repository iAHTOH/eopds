# Точка останова (2026-11-20)

Записываемся здесь, чтобы завтра продолжить с этого места. Ветка `dev`,
последний коммит: `feat(scan): фоновое сканирование с живым прогрессом`.

## Где мы / что сделано (сегодня)

1. **Полный отказ от Python/Django** (коммит ≪chore: переход на Go-only≫):
   удалён весь старый проект и legacy-мусор. Канонический план — [roadmap.md](roadmap.md).
2. **Сканер: фоновый режим + прогресс.** `POST /scan/run` теперь запускает
   скан в горутине (`context.Background`), UI показывает живой счётчик.
   Проверено на коллекции `/books`: 6211 книг, 6210 создано, 0 ошибок.
   См. [go/scanner.md](go/scanner.md), [go/api.md](go/api.md).

Библиотека очищена и пересканирована заново; папка `Linux/` (zip с FB2)
распознана сканером и присутствует в каталогах.

3. **Батч-импорт (ускорение скана).** Импорт пишется пачками (одна транзакция
   на ~500 книг + per-book SAVEPOINT + кэш справочников). Замер на 6210 книг:
   первичный ~11 с (было ~58 с), повторный ~8 с (≈5–7x). Тесты
   `TestImportBooksBatch`, `TestImportBooksErrorIsolation`.
   См. [go/scanner.md](go/scanner.md). Версия → 0.49.0.
4. **Инкрементальный скан.** Отпечатки `size`+`mtime` прочитанных файлов/архивов
   в таблице `scan_files` (миграция, schema_version 3). Неизменившиеся
   источники пропускаются без парсинга. Повторный скан на 6210 книг:
   `skipped=8`, `found=0`, **< 1 с** (было ~8 с). UI: чекбокс «Полный скан»
   (`full=1`) для принудительного перечитывания. Тесты
   `TestScanFilesRoundTrip`, `TestClearLibraryClearsScanFiles`,
   `TestWalkIncrementalSkipsUnchanged`, `TestWalkZipArchiveFingerprint`.
   См. [go/scanner.md](go/scanner.md#инкрементальный-скан). Версия → 0.50.0.

## С чего начнём завтра (next)

1. Дальше по этапам — онлайн-читалка / конвертер FB2→EPUB/MOBI (на Go),
   публичный OPDS-портал, телеграм-бот (всё заново, см. roadmap.md).

## Полезное для быстрого старта

```bash
# поднять стек
docker compose -f docker-compose.go.yml up -d
# сборка образа после правок Go
docker compose -f docker-compose.go.yml build eopds-go && \
  docker compose -f docker-compose.go.yml up -d eopds-go
```

Логин админа в dev-стенде: `admin` / `password123`.
Скан-каталог: `/books` (в контейнере), монтируется из
`/run/media/ahtoh/SSD2/e-book`.

Проверка статуса сканирования (после логина, cookie `eopds_session`):
`GET http://localhost:8080/scan/status`.

## Технические заметки (грабли)

- **edit-инструмент иногда «съедает» начало файла** при больших правках —
  после правок Go-файлов всегда проверять `go build ./...` и `gofmt -l`.
  read_file может отдавать устаревшее содержимое — доверять `grep`/`go build`.
- Скан **не должен** брать `r.Context()`: только `context.Background()`.
