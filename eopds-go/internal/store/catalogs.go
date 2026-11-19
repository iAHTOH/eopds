package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Catalog — узел каталога (папка или zip). Kind: 0=dir, 1=zip, 2=inpx.
type Catalog struct {
	ID         int64
	ParentID   int64 // 0 — корень
	Name       string
	Path       string
	Kind       int
	Books      int64 // количество книг непосредственно в этом узле
	Children   int64 // количество прямых дочерних каталогов/папок
}

// CatalogPage — содержимое каталога: подэлементы и книги.
type CatalogPage struct {
	Catalog  Catalog
	Children []Catalog
	Books    LibraryPage
}

// ListRootCatalogs возвращает каталоги верхнего уровня (parent_id IS NULL),
// упорядоченные по имени. Каждый содержит число книг и детей.
func (s *DBStore) ListRootCatalogs(ctx context.Context) ([]Catalog, error) {
	q := "SELECT c.id, COALESCE(c.parent_id,0), c.name, c.path, c.kind, " +
		"(SELECT COUNT(*) FROM books b WHERE b.catalog_id = c.id), " +
		"(SELECT COUNT(*) FROM catalogs cc WHERE cc.parent_id = c.id) " +
		"FROM catalogs c WHERE c.parent_id IS NULL ORDER BY c.name"
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: root catalogs: %w", err)
	}
	defer rows.Close()
	var out []Catalog
	for rows.Next() {
		var c Catalog
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Path, &c.Kind, &c.Books, &c.Children); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCatalog возвращает узел каталога по id.
func (s *DBStore) GetCatalog(ctx context.Context, id int64) (Catalog, error) {
	var c Catalog
	q := "SELECT c.id, COALESCE(c.parent_id,0), c.name, c.path, c.kind, " +
		"(SELECT COUNT(*) FROM books b WHERE b.catalog_id = c.id), " +
		"(SELECT COUNT(*) FROM catalogs cc WHERE cc.parent_id = c.id) " +
		"FROM catalogs c WHERE c.id = " + s.ph(1)
	err := s.db.QueryRowContext(ctx, q, id).Scan(&c.ID, &c.ParentID, &c.Name, &c.Path, &c.Kind, &c.Books, &c.Children)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Catalog{}, ErrNotFound
		}
		return Catalog{}, err
	}
	return c, nil
}

// ListChildCatalogs возвращает прямые подэлементы каталога (подпапки/zip),
// упорядоченные: сначала папки, затем zip, внутри — по имени.
func (s *DBStore) ListChildCatalogs(ctx context.Context, parentID int64) ([]Catalog, error) {
	q := "SELECT c.id, COALESCE(c.parent_id,0), c.name, c.path, c.kind, " +
		"(SELECT COUNT(*) FROM books b WHERE b.catalog_id = c.id), " +
		"(SELECT COUNT(*) FROM catalogs cc WHERE cc.parent_id = c.id) " +
		"FROM catalogs c WHERE c.parent_id = " + s.ph(1) + " ORDER BY c.kind, c.name"
	rows, err := s.db.QueryContext(ctx, q, parentID)
	if err != nil {
		return nil, fmt.Errorf("store: child catalogs: %w", err)
	}
	defer rows.Close()
	var out []Catalog
	for rows.Next() {
		var c Catalog
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Path, &c.Kind, &c.Books, &c.Children); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListBooksInCatalog возвращает книги, лежащие непосредственно в узле
// (catalog_id = id), постранично, по алфавиту названия.
func (s *DBStore) ListBooksInCatalog(ctx context.Context, catalogID int64, page, size int) (LibraryPage, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 500 {
		size = 500
	}
	var total int64
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM books WHERE catalog_id = "+s.ph(1), catalogID).Scan(&total); err != nil {
		return LibraryPage{}, fmt.Errorf("store: catalog books count: %w", err)
	}
	offset := (page - 1) * size
	q := "SELECT id, title, format, size_bytes, lang, docdate, annotation, path, filename " +
		"FROM books WHERE catalog_id = " + s.ph(1) +
		" ORDER BY search_title, id LIMIT " + s.ph(2) + " OFFSET " + s.ph(3)
	rows, err := s.db.QueryContext(ctx, q, catalogID, size, offset)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("store: catalog books: %w", err)
	}
	defer rows.Close()
	var items []LibraryBook
	for rows.Next() {
		var b LibraryBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Format, &b.SizeBytes, &b.Lang, &b.DocDate,
			&b.Annotation, &b.Path, &b.Filename); err != nil {
			return LibraryPage{}, err
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return LibraryPage{}, err
	}
	// Связи (авторы/жанры/серии) — для отображения в списке.
	if err := s.fillBookRelations(ctx, items); err != nil {
		return LibraryPage{}, err
	}
	pages := int((total + int64(size) - 1) / int64(size))
	return LibraryPage{Items: items, Total: total, Page: page, Size: size, Pages: pages}, nil
}
