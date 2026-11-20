package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/config"
)

// AuthorRef — ссылка на автора (для ссылок в UI).
type AuthorRef struct {
	ID   int64
	Name string
}

// GenreRef — ссылка на жанр (для ссылок в UI).
type GenreRef struct {
	ID   int64
	Code string
}

// SeriesRef — ссылка на серию и номер книги в ней.
type SeriesRef struct {
	ID    int64
	Title string
	Seq   int
}

// LibraryBook — книга для списков библиотеки (с агрегированными связями).
type LibraryBook struct {
	ID         int64
	Title      string
	Format     string
	SizeBytes  int64
	Lang       string
	DocDate    string
	Annotation string
	Path       string
	Filename   string
	Authors    []AuthorRef
	Genres     []GenreRef
	Series     []SeriesRef
}

// AlphabetItem — элемент алфавитного указателя (первый символ + количество).
type AlphabetItem struct {
	Key   string // первый символ (или несколько, если задан префикс)
	Count int64
}

// LibraryPage — результат постраничной выборки.
type LibraryPage struct {
	Items []LibraryBook
	Total int64
	Page  int
	Size  int
	Pages int
}

// BooksFilter — параметры выборки книг.
type BooksFilter struct {
	// Letter — фильтр по началу search_title (пусто — без фильтра).
	Letter string
	// AuthorID — фильтр по автору (0 — без фильтра).
	AuthorID int64
	// SeriesID — фильтр по серии (0 — без фильтра).
	SeriesID int64
	// GenreID — фильтр по жанру (0 — без фильтра).
	GenreID int64
	// LangCode — фильтр по языку (0 — все; 1 кириллица, 2 латиница, 3 цифры, 9 прочее).
	LangCode int
}

func (f BooksFilter) where(s *DBStore, args *[]any) string {
	var conds []string
	if f.LangCode != 0 {
		*args = append(*args, f.LangCode)
		conds = append(conds, "b.lang_code = "+s.ph(len(*args)))
	}
	if f.Letter != "" {
		*args = append(*args, strings.ToLower(f.Letter)+"%")
		conds = append(conds, "b.search_title LIKE "+s.ph(len(*args)))
	}
	if f.AuthorID != 0 {
		*args = append(*args, f.AuthorID)
		conds = append(conds, "EXISTS (SELECT 1 FROM book_authors ba WHERE ba.book_id=b.id AND ba.author_id="+s.ph(len(*args))+")")
	}
	if f.SeriesID != 0 {
		*args = append(*args, f.SeriesID)
		conds = append(conds, "EXISTS (SELECT 1 FROM book_series bs WHERE bs.book_id=b.id AND bs.series_id="+s.ph(len(*args))+")")
	}
	if f.GenreID != 0 {
		*args = append(*args, f.GenreID)
		conds = append(conds, "EXISTS (SELECT 1 FROM book_genres bg WHERE bg.book_id=b.id AND bg.genre_id="+s.ph(len(*args))+")")
	}
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}

// ListBooks возвращает страницу книг по фильтру с общим количеством.
// page начинается с 1; size ограничен (>=1, <=500).
func (s *DBStore) ListBooks(ctx context.Context, f BooksFilter, page, size int) (LibraryPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	var args []any
	where := f.where(s, &args)

	countQ := "SELECT COUNT(*) FROM books b" + where
	var total int64
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return LibraryPage{}, fmt.Errorf("store: books count: %w", err)
	}

	offset := (page - 1) * size
	// Плейсхолдеры: сначала WHERE-аргументы, затем LIMIT/OFFSET.
	limitPh := s.ph(len(args) + 1)
	offsetPh := s.ph(len(args) + 2)
	listArgs := append(append([]any{}, args...), size, offset)
	listQ := "SELECT id, title, format, size_bytes, lang, docdate, annotation, path, filename " +
		"FROM books b" + where +
		" ORDER BY b.search_title, b.id LIMIT " + limitPh + " OFFSET " + offsetPh

	rows, err := s.db.QueryContext(ctx, listQ, listArgs...)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("store: books list: %w", err)
	}
	defer rows.Close()

	var items []LibraryBook
	for rows.Next() {
		var b LibraryBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang,
			&b.DocDate, &b.Annotation, &b.Path, &b.Filename); err != nil {
			return LibraryPage{}, fmt.Errorf("store: book scan: %w", err)
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return LibraryPage{}, err
	}

	if err := s.fillBookRelations(ctx, items); err != nil {
		return LibraryPage{}, err
	}

	pages := int((total + int64(size) - 1) / int64(size))
	return LibraryPage{Items: items, Total: total, Page: page, Size: size, Pages: pages}, nil
}

// fillBookRelations дозагружает авторов, жанры и серии для набора книг.
func (s *DBStore) fillBookRelations(ctx context.Context, items []LibraryBook) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]any, len(items))
	idx := make(map[int64]int, len(items))
	for i := range items {
		ids[i] = items[i].ID
		idx[items[i].ID] = i
	}
	placeholders := s.inList(len(ids))

	// Авторы.
	aq := "SELECT ba.book_id, a.id, a.full_name FROM book_authors ba JOIN authors a ON a.id=ba.author_id " +
		"WHERE ba.book_id IN (" + placeholders + ") ORDER BY a.full_name"
	if err := s.collectRefs(ctx, aq, ids, idx, items, func(i int, id int64, v string) {
		items[i].Authors = append(items[i].Authors, AuthorRef{ID: id, Name: v})
	}); err != nil {
		return err
	}
	// Жанры.
	gq := "SELECT bg.book_id, g.id, g.code FROM book_genres bg JOIN genres g ON g.id=bg.genre_id " +
		"WHERE bg.book_id IN (" + placeholders + ") ORDER BY g.code"
	if err := s.collectRefs(ctx, gq, ids, idx, items, func(i int, id int64, v string) {
		items[i].Genres = append(items[i].Genres, GenreRef{ID: id, Code: v})
	}); err != nil {
		return err
	}
	// Серии (+номер книги в серии).
	sq := "SELECT bs.book_id, se.id, se.title, bs.seq_no FROM book_series bs JOIN series se ON se.id=bs.series_id " +
		"WHERE bs.book_id IN (" + placeholders + ") ORDER BY se.title"
	rows, err := s.db.QueryContext(ctx, sq, ids...)
	if err != nil {
		return fmt.Errorf("store: book series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bookID, seriesID, seq int64
		var title string
		if err := rows.Scan(&bookID, &seriesID, &title, &seq); err != nil {
			return err
		}
		if i, ok := idx[bookID]; ok {
			items[i].Series = append(items[i].Series, SeriesRef{ID: seriesID, Title: title, Seq: int(seq)})
		}
	}
	return rows.Err()
}

// collectRefs выполняет запрос (book_id, ref_id, value) и раскладывает ссылки
// по книгам через add. Используется для авторов и жанров.
func (s *DBStore) collectRefs(ctx context.Context, q string, ids []any, idx map[int64]int,
	items []LibraryBook, add func(int, int64, string)) error {
	rows, err := s.db.QueryContext(ctx, q, ids...)
	if err != nil {
		return fmt.Errorf("store: book relations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bookID, refID int64
		var v string
		if err := rows.Scan(&bookID, &refID, &v); err != nil {
			return err
		}
		if i, ok := idx[bookID]; ok {
			add(i, refID, v)
		}
	}
	return rows.Err()
}

// inList строит список плейсхолдеров через запятую.
func (s *DBStore) inList(n int) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = s.ph(i + 1)
	}
	return strings.Join(parts, ", ")
}

// BookAlphabet возвращает алфавитный указатель книг по первому символу search_title.
func (s *DBStore) BookAlphabet(ctx context.Context) ([]AlphabetItem, error) {
	return s.alphabet(ctx,
		"SELECT SUBSTR(search_title,1,1) AS k, COUNT(*) FROM books "+
			"WHERE search_title <> '' GROUP BY k ORDER BY k")
}

// AuthorAlphabet — указатель авторов по search_full_name.
func (s *DBStore) AuthorAlphabet(ctx context.Context) ([]AlphabetItem, error) {
	return s.alphabet(ctx,
		"SELECT SUBSTR(search_full_name,1,1) AS k, COUNT(*) FROM authors "+
			"WHERE search_full_name <> '' GROUP BY k ORDER BY k")
}

// SeriesAlphabet — указатель серий по search_title.
func (s *DBStore) SeriesAlphabet(ctx context.Context) ([]AlphabetItem, error) {
	return s.alphabet(ctx,
		"SELECT SUBSTR(search_title,1,1) AS k, COUNT(*) FROM series "+
			"WHERE search_title <> '' GROUP BY k ORDER BY k")
}

// alphabet выполняет запрос вида (key, count).
func (s *DBStore) alphabet(ctx context.Context, q string, args ...any) ([]AlphabetItem, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: alphabet: %w", err)
	}
	defer rows.Close()
	var out []AlphabetItem
	for rows.Next() {
		var it AlphabetItem
		if err := rows.Scan(&it.Key, &it.Count); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SearchBooks ищет книги по подстроке в названии (case-insensitive), постранично.
func (s *DBStore) SearchBooks(ctx context.Context, q string, page, size int) (LibraryPage, error) {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return LibraryPage{Page: 1, Size: size}, nil
	}
	// Переиспользуем ListBooks с фильтром по подстроке нельзя (там prefix),
	// поэтому отдельный запрос.
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	like := "%" + q + "%"
	var total int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM books WHERE search_title LIKE "+s.ph(1), like).Scan(&total); err != nil {
		return LibraryPage{}, fmt.Errorf("store: search count: %w", err)
	}
	offset := (page - 1) * size
	listQ := "SELECT id, title, format, size_bytes, lang, docdate, annotation, path, filename " +
		"FROM books WHERE search_title LIKE " + s.ph(1) +
		" ORDER BY search_title, id LIMIT " + s.ph(2) + " OFFSET " + s.ph(3)
	rows, err := s.db.QueryContext(ctx, listQ, like, size, offset)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("store: search list: %w", err)
	}
	defer rows.Close()
	var items []LibraryBook
	for rows.Next() {
		var b LibraryBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang,
			&b.DocDate, &b.Annotation, &b.Path, &b.Filename); err != nil {
			return LibraryPage{}, err
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return LibraryPage{}, err
	}
	if err := s.fillBookRelations(ctx, items); err != nil {
		return LibraryPage{}, err
	}
	pages := int((total + int64(size) - 1) / int64(size))
	return LibraryPage{Items: items, Total: total, Page: page, Size: size, Pages: pages}, nil
}

// NamedItem — автор/серия/жанр для списков библиотеки.
type NamedItem struct {
	ID    int64
	Title string
	Count int64
}

// ListAuthors возвращает авторов постранично (по алфавиту) с числом книг.
// prefix — фильтр по началу search_full_name (пусто — все).
func (s *DBStore) ListAuthors(ctx context.Context, prefix string, page, size int) (items []NamedItem, total int64, err error) {
	return s.listNamed(ctx, "authors", "search_full_name", "full_name", "book_authors", "author_id", prefix, page, size)
}

// ListSeries возвращает серии постранично.
func (s *DBStore) ListSeries(ctx context.Context, prefix string, page, size int) (items []NamedItem, total int64, err error) {
	return s.listNamed(ctx, "series", "search_title", "title", "book_series", "series_id", prefix, page, size)
}

// listNamed — обобщённый листинг сущности (table/sortCol/labelCol/joinTable/joinCol).
func (s *DBStore) listNamed(ctx context.Context, table, sortCol, labelCol, joinTable, joinCol,
	prefix string, page, size int) ([]NamedItem, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	where := ""
	var args []any
	if prefix != "" {
		args = append(args, strings.ToLower(prefix)+"%")
		where = " WHERE t." + sortCol + " LIKE " + s.ph(1)
	}
	var total int64
	countQ := "SELECT COUNT(*) FROM " + table + " t" + where
	if err := s.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: %s count: %w", table, err)
	}
	offset := (page - 1) * size
	listQ := "SELECT t.id, t." + labelCol + ", " +
		"(SELECT COUNT(*) FROM " + joinTable + " j WHERE j." + joinCol + "=t.id) AS cnt " +
		"FROM " + table + " t" + where +
		" ORDER BY t." + sortCol + ", t.id LIMIT " + s.ph(len(args)+1) + " OFFSET " + s.ph(len(args)+2)
	listArgs := append(append([]any{}, args...), size, offset)
	rows, err := s.db.QueryContext(ctx, listQ, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: %s list: %w", table, err)
	}
	defer rows.Close()
	var out []NamedItem
	for rows.Next() {
		var it NamedItem
		if err := rows.Scan(&it.ID, &it.Title, &it.Count); err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// ListGenres возвращает жанры постранично с числом книг.
func (s *DBStore) ListGenres(ctx context.Context, page, size int) ([]NamedItem, int64, error) {
	return s.listNamed(ctx, "genres", "code", "code", "book_genres", "genre_id", "", page, size)
}

// GetBook возвращает одну книгу со связями по id (ErrNotFound, если нет).
func (s *DBStore) GetBook(ctx context.Context, id int64) (LibraryBook, error) {
	var b LibraryBook
	row := s.db.QueryRowContext(ctx,
		"SELECT id, title, format, size_bytes, lang, docdate, annotation, path, filename FROM books WHERE id="+s.ph(1), id)
	if err := row.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang,
		&b.DocDate, &b.Annotation, &b.Path, &b.Filename); err != nil {
		if err == sql.ErrNoRows {
			return LibraryBook{}, ErrNotFound
		}
		return LibraryBook{}, err
	}
	items := []LibraryBook{b}
	if err := s.fillBookRelations(ctx, items); err != nil {
		return LibraryBook{}, err
	}
	return items[0], nil
}

// EntityName возвращает человекочитаемое название сущности по виду и id.
// kind: "author" | "series" | "genre". Пустая строка, если не найдено.
func (s *DBStore) EntityName(ctx context.Context, kind string, id int64) (string, error) {
	var table, col string
	switch kind {
	case "author":
		table, col = "authors", "full_name"
	case "series":
		table, col = "series", "title"
	case "genre":
		table, col = "genres", "code"
	default:
		return "", nil
	}
	var name string
	q := "SELECT " + col + " FROM " + table + " WHERE id=" + s.ph(1)
	err := s.db.QueryRowContext(ctx, q, id).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: entity name: %w", err)
	}
	return name, nil
}

// ---------------------------------------------------------------------------
// Префиксный алфавитный указатель (как в старом web-интерфейсе).
// ---------------------------------------------------------------------------

// LangFilter — условие по lang_code для таблицы (пусто, если 0 = все).
func langCond(s *DBStore, col string, lang int, args *[]any) string {
	if lang == 0 {
		return ""
	}
	*args = append(*args, lang)
	return " AND " + col + " = " + s.ph(len(*args))
}

// PrefixIndex — группировка по первым `length` символам колонки col таблицы table
// в пределах префикса `prefix` (может быть пустым) и фильтра по языку.
//
// Возвращает элементы (код префикса длиной length, число записей), которые
// начинаются с `prefix`. Используется для построения многоуровневого указателя.
func (s *DBStore) PrefixIndex(ctx context.Context, table, col string, length int,
	prefix string, lang int) ([]AlphabetItem, error) {
	if length < 1 {
		length = 1
	}
	var args []any
	where := col + " <> ''"
	if prefix != "" {
		args = append(args, strings.ToLower(prefix)+"%")
		where += " AND " + col + " LIKE " + s.ph(len(args))
	}
	where += langCond(s, "lang_code", lang, &args)

	// SUBSTR(col,1,length): плейсхолдеры в PG не работают в length, но число
	// — константа, подставляем как литерал (safely, это int).
	q := fmt.Sprintf("SELECT SUBSTR(%s,1,%d) AS k, COUNT(*) FROM %s WHERE %s GROUP BY k ORDER BY k",
		col, length, table, where)
	return s.alphabet(ctx, q, args...)
}

// ListBooksByPrefix возвращает страницу книг, search_title которых начинается
// с prefix, с фильтром по языку.
func (s *DBStore) ListBooksByPrefix(ctx context.Context, prefix string, lang, page, size int) (LibraryPage, error) {
	f := BooksFilter{Letter: prefix, LangCode: lang}
	return s.ListBooks(ctx, f, page, size)
}

// ListAuthorsByPrefix — авторы по префиксу search_full_name с числом книг.
func (s *DBStore) ListAuthorsByPrefix(ctx context.Context, prefix string, page, size int) ([]NamedItem, int64, error) {
	return s.listNamedPrefix(ctx, "authors", "search_full_name", "full_name", "book_authors", "author_id", prefix, page, size)
}

// ListSeriesByPrefix — серии по префиксу search_title с числом книг.
func (s *DBStore) ListSeriesByPrefix(ctx context.Context, prefix string, page, size int) ([]NamedItem, int64, error) {
	return s.listNamedPrefix(ctx, "series", "search_title", "title", "book_series", "series_id", prefix, page, size)
}

// listNamedPrefix — как listNamed, но для чистого префикса.
func (s *DBStore) listNamedPrefix(ctx context.Context, table, sortCol, labelCol, joinTable, joinCol,
	prefix string, page, size int) ([]NamedItem, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	var args []any
	where := " WHERE t." + sortCol + " <> ''"
	if prefix != "" {
		args = append(args, strings.ToLower(prefix)+"%")
		where += " AND t." + sortCol + " LIKE " + s.ph(len(args))
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" t"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: %s count: %w", table, err)
	}
	offset := (page - 1) * size
	listQ := "SELECT t.id, t." + labelCol + ", " +
		"(SELECT COUNT(*) FROM " + joinTable + " j WHERE j." + joinCol + "=t.id) AS cnt " +
		"FROM " + table + " t" + where +
		" ORDER BY t." + sortCol + ", t.id LIMIT " + s.ph(len(args)+1) + " OFFSET " + s.ph(len(args)+2)
	listArgs := append(append([]any{}, args...), size, offset)
	rows, err := s.db.QueryContext(ctx, listQ, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: %s list: %w", table, err)
	}
	defer rows.Close()
	var out []NamedItem
	for rows.Next() {
		var it NamedItem
		if err := rows.Scan(&it.ID, &it.Title, &it.Count); err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// BookByPrefixAlphabet — книги по префиксу (для указателя книг).
func (s *DBStore) BookByPrefixIndex(ctx context.Context, length int, prefix string, lang int) ([]AlphabetItem, error) {
	return s.PrefixIndex(ctx, "books", "search_title", length, prefix, lang)
}

// RandomBook возвращает случайную книгу (ErrNotFound, если книг нет).
func (s *DBStore) RandomBook(ctx context.Context) (LibraryBook, error) {
	var id int64
	q := "SELECT id FROM books ORDER BY RANDOM() LIMIT 1"
	if s.dialectIsPG() {
		q = "SELECT id FROM books ORDER BY random() LIMIT 1"
	}
	if err := s.db.QueryRowContext(ctx, q).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return LibraryBook{}, ErrNotFound
		}
		return LibraryBook{}, err
	}
	return s.GetBook(ctx, id)
}

// RecentBooks возвращает последние добавленные книги (по id desc).
func (s *DBStore) RecentBooks(ctx context.Context, limit int) ([]LibraryBook, error) {
	if limit < 1 {
		limit = 3
	}
	q := "SELECT id, title, format, size_bytes, lang, docdate, annotation, path, filename " +
		"FROM books ORDER BY id DESC LIMIT " + s.ph(1)
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent books: %w", err)
	}
	defer rows.Close()
	var items []LibraryBook
	for rows.Next() {
		var b LibraryBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang,
			&b.DocDate, &b.Annotation, &b.Path, &b.Filename); err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.fillBookRelations(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

// dialectIsPG сообщает, используется ли PostgreSQL.
func (s *DBStore) dialectIsPG() bool {
	return s.kind == config.DBPostgres
}
