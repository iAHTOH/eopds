// Package meta описывает контракт обмена между Go-сканером и Python-адаптером.
//
// Обмен идёт построчно в формате JSON Lines (по одному объекту на строку)
// через stdout. Логи и диагностика — отдельно, в stderr (см. пакет logx),
// чтобы не смешивать с потоком данных.
//
// Формат намеренно «плоский» и стабильный: Python-сторона читает строки
// и вызывает существующую ORM-логику (opdsdb.addbook/addauthor/...).
// Здесь НЕТ ничего про БД — только извлечённые из файлов метаданные.
package meta

// RecordKind — тип записи в потоке JSON Lines.
type RecordKind string

const (
	// KindBook — одна распознанная книга (файл на диске или запись в ZIP).
	KindBook RecordKind = "book"
	// KindArchiveStart — начало обработки ZIP-архива.
	KindArchiveStart RecordKind = "archive_start"
	// KindArchiveEnd — завершение обработки ZIP-архива.
	KindArchiveEnd RecordKind = "archive_end"
	// KindError — некритичная ошибка по конкретному файлу/архиву.
	KindError RecordKind = "error"
	// KindStats — итоговая статистика по завершении сканирования.
	KindStats RecordKind = "stats"
)

// Fingerprint — отпечаток файла для инкрементального скана.
// Size — размер в байтах, MTime — unix-время модификации в наносекундах.
type Fingerprint struct {
	Size  int64 `json:"size"`
	MTime int64 `json:"mtime"`
}

// Series — информация о серии (если книга входит в серию).
type Series struct {
	Title string `json:"title,omitempty"`
	Index int    `json:"index,omitempty"`
}

// BookData — метаданные книги, извлечённые парсером конкретного формата
// (например, FB2). Это внутренний результат парсера, который затем
// превращается в Record{Kind: KindBook}.
type BookData struct {
	Title      string
	Lang       string
	DocDate    string
	Annotation string
	Genres     []string
	Authors    []string
	Series     *Series
}

// Record — одна строка потока JSON Lines.
//
// Поля partially опускаются (omitempty), чтобы поток был компактным.
// Поле Kind всегда присутствует.
type Record struct {
	Kind RecordKind `json:"kind"`

	// --- Поля для KindBook ---
	// CatalogType: 0=обычный каталог (dir), 1=ZIP, 2=INPX/INP.
	CatalogType int `json:"catalog_type,omitempty"`
	// RelPath — путь относительно EOPDS_ROOT_LIB.
	// Для файла на диске это каталог, в котором лежит книга.
	// Для книги в ZIP это путь к каталогу с архивом.
	RelPath  string `json:"rel_path,omitempty"`
	Filename string `json:"filename,omitempty"`
	Format   string `json:"format,omitempty"`
	Size     int64  `json:"size,omitempty"`
	// MTime — unix-время модификации файла/архива в наносекундах (инкрем. скан).
	MTime int64 `json:"mtime,omitempty"`

	Title      string `json:"title,omitempty"`
	Lang       string `json:"lang,omitempty"`
	DocDate    string `json:"docdate,omitempty"`
	Annotation string `json:"annotation,omitempty"`

	Authors []string `json:"authors,omitempty"`
	Genres  []string `json:"genres,omitempty"`
	Series  *Series  `json:"series,omitempty"`

	// BookPath — путь для БД (для записи book.path).
	// Для файла на диске совпадает с RelPath; для ZIP это путь к архиву
	// (как в текущем Python-сканере: book.path = путь к .zip).
	BookPath string `json:"book_path,omitempty"`

	// --- Поля для KindArchiveStart/End ---
	ArchivePath string `json:"archive_path,omitempty"`

	// --- Поля для KindError ---
	Error string `json:"error,omitempty"`

	// --- Поля для KindStats ---
	BooksFound   int64   `json:"books_found,omitempty"`
	BooksParsed  int64   `json:"books_parsed,omitempty"`
	ArchivesSeen int64   `json:"archives_seen,omitempty"`
	BadBooks     int64   `json:"bad_books,omitempty"`
	BadArchives  int64   `json:"bad_archives,omitempty"`
	ElapsedMS    float64 `json:"elapsed_ms,omitempty"`
	Workers      int     `json:"workers,omitempty"`
	// Skipped — сколько файлов/архивов пропущено как неизменившиеся.
	Skipped int64 `json:"skipped,omitempty"`
}
