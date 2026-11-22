# Конвертер EPUB

Конвертация книг FB2 в **EPUB** (2.0 и 3.0) «на лету» при скачивании.
Вёрстку тела выполняет `internal/fb2render` (FB2 → HTML), упаковку в
EPUB-контейнер — пакет `internal/epub`.

## Что поддерживается

| `fmt` | Формат | EPub `version` | Расширение файла |
|---|---|---|---|
| `epub`, `epub3` | EPUB 3 | `3.0` | `.epub` |
| `epub2` | EPUB 2 | `2.0` | `.epub` |
| `kepub` | Kobo EPUB | `3.0` | `.kepub.epub` |

Конвертация доступна **только для FB2** (`book.Format == "fb2"`); для прочих
исходных форматов `fmt` конвертации даёт `404`.

## Kobo EPUB (kepub)

`?fmt=kepub` собирает EPUB3 с **Kobo-разметкой**:

- текст в `<p>` и `<h1..h6>` оборачивается в `<span class="koboSpan"
  id="kobo.P.1">…</span>` (`P` — номер блока по порядку) — нужно Kobo для
  пагинации и закладок;
- добавляются `OEBPS/kobo.js` и `OEBPS/kobo.css`, подключаются в `<head>` и
  регистрируются в `manifest`;
- файл отдаётся с расширением `.kepub.epub`.

Разметка применяется **только** для EPUB3 (для `epub2` игнорируется).
Обработка идемпотентна (повторный прогон не задваивает span), пустые блоки и
блоки с картинками/таблицами не оборачиваются, `<pre>` не считается блочным.

Не поддерживаются (по решению — [roadmap](../roadmap.md)): AZW8/KFX (закрытый
формат Amazon), PDF (плохой результат из переформатируемого FB2), TXT/MD
(опционально позже). Для них `fmt` даёт `404`.

## Использование

```bash
# EPUB 3
curl -b cookie.txt -o book.epub \
  'http://SERVER:8080/library/book/123/download?fmt=epub'

# EPUB 2
curl -b cookie.txt -o book.epub \
  'http://SERVER:8080/library/book/123/download?fmt=epub2'

# Kobo
curl -b cookie.txt -o book.kepub.epub \
  'http://SERVER:8080/library/book/123/download?fmt=kepub'
```

Ответ: `Content-Type: application/epub+zip`, `Content-Disposition: attachment`
с именем `<название>.epub`.

В [OPDS](opds.md) для FB2-книг добавляется acquisition-ссылка
`?fmt=epub` (`application/epub+zip`) рядом с исходным файлом и FB2-zip.

## Структура EPUB

```
mimetype                      (первым, без сжатия: "application/epub+zip")
META-INF/container.xml
OEBPS/
  content.opf                 (метаданные, manifest, spine)
  nav.xhtml                   (оглавление EPUB3)
  toc.ncx                     (оглавление EPUB2/совместимость)
  content.xhtml               (тело книги, XHTML)
  style.css                   (базовые стили)
  kobo.js, kobo.css           (только kepub)
  images/<id>                 (встроенные картинки; media-type в manifest)
```

- `mimetype` пишется первым и **без сжатия** (требование спецификации).
- Все XHTML/OPF/NCX — **well-formed XML**. Поэтому `fb2render` генерирует
  самозакрывающиеся void-элементы (`<br/>`, `<img/>`), а текст экранируется
  (`html.EscapeString`), чтобы EPUB не отверг контент.
- Картинки выносятся в файлы `images/<id>` (имя = `epub.ImageHref(id)`),
  ссылки в теле совпадают с файлами; `media-type` берётся из `<binary>` FB2.
- Метаданные OPF: `dc:title`, `dc:creator` (авторы), `dc:language`,
  `dc:subject` (жанры), `dc:description` (аннотация, без тегов),
  `dcterms:modified` (EPUB3).

## Устройство кода

- `internal/fb2render` — FB2 → HTML-фрагмент + оглавление + список картинок
  (`Result.Images`). `Options.ImageURL` позволяет заменить data-URI на путь к
  файлу; для EPUB передаётся `epub.ImageHref`.
- `internal/epub` — `Build(Book, Options) ([]byte, error)`: собирает ZIP.
  Пакет не зависит от store (нейтральные данные на входе).
- `internal/admin/epub_export.go` — `bookToEPUB(ctx, store, book, version)`:
  читает исходный FB2 (`BookFile`), рендерит, собирает EPUB.
- `handleBookDownload` — маршрутизация `fmt` → EPUB.

## Тесты

- `internal/epub/kobo_test.go` — koboSpan-разметка, kepub-сборка, EPUB2-игнор.
- `internal/epub/epub_test.go` — структура архива (`mimetype` первым без
  сжатия), обязательные файлы, валидность XML, EPUB2/3, экранирование, MIME.
- `internal/fb2render/render_test.go` — XHTML-совместимость (void-элементы).
