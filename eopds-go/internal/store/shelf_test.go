package store

import (
	"context"
	"testing"
)

// importBook — вспомогательная функция: импортирует одну книгу и возвращает id.
func importBook(t *testing.T, st Store, rel, title string) int64 {
	t.Helper()
	r, err := st.ImportBook(context.Background(), IngestBook{
		RelPath:  rel,
		BookPath: rel,
		Filename: title + ".fb2",
		Format:   "fb2",
		Size:     128,
		Title:    title,
		Lang:     "ru",
	})
	if err != nil {
		t.Fatalf("import %q: %v", title, err)
	}
	id, err := st.GetBook(context.Background(), r.BookID)
	if err != nil {
		t.Fatalf("get book %q: %v", title, err)
	}
	return id.ID
}

func TestShelfAddRemove(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	uid, err := st.CreateRootAdmin(ctx, "u1", "hash", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	b1 := importBook(t, st, "/lib/a", "Книга А")
	b2 := importBook(t, st, "/lib/b", "Книга Б")

	// Пустая полка.
	pg, err := st.ShelfBooks(ctx, uid, 1, 10)
	if err != nil {
		t.Fatalf("shelf empty: %v", err)
	}
	if pg.Total != 0 || len(pg.Items) != 0 {
		t.Fatalf("ожидалась пустая полка, получено total=%d items=%d", pg.Total, len(pg.Items))
	}
	n, err := st.CountShelf(ctx, uid)
	if err != nil || n != 0 {
		t.Fatalf("CountShelf пусто: n=%d err=%v", n, err)
	}

	// Добавляем две книги; последняя добавленная должна быть сверху.
	if err := st.AddToShelf(ctx, uid, b1); err != nil {
		t.Fatalf("add b1: %v", err)
	}
	if err := st.AddToShelf(ctx, uid, b2); err != nil {
		t.Fatalf("add b2: %v", err)
	}
	pg, err = st.ShelfBooks(ctx, uid, 1, 10)
	if err != nil {
		t.Fatalf("shelf: %v", err)
	}
	if pg.Total != 2 || len(pg.Items) != 2 {
		t.Fatalf("ожидалось 2 книги, получено total=%d items=%d", pg.Total, len(pg.Items))
	}
	if pg.Items[0].ID != b2 {
		t.Fatalf("сверху должна быть последняя добавленная (id=%d), получено id=%d", b2, pg.Items[0].ID)
	}

	// Повторное добавление не дублирует.
	if err := st.AddToShelf(ctx, uid, b1); err != nil {
		t.Fatalf("re-add b1: %v", err)
	}
	n, _ = st.CountShelf(ctx, uid)
	if n != 2 {
		t.Fatalf("после повторного добавления ожидалось 2, получено %d", n)
	}

	// Удаление с полки.
	if err := st.RemoveFromShelf(ctx, uid, b1); err != nil {
		t.Fatalf("remove b1: %v", err)
	}
	n, _ = st.CountShelf(ctx, uid)
	if n != 1 {
		t.Fatalf("после удаления ожидалось 1, получено %d", n)
	}
	// Идемпотентность: повторное удаление не ошибка.
	if err := st.RemoveFromShelf(ctx, uid, b1); err != nil {
		t.Fatalf("remove b1 повторно: %v", err)
	}
}

func TestShelfIsolatedPerUser(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u1, _ := st.CreateRootAdmin(ctx, "u1", "hash", "")
	b1 := importBook(t, st, "/lib/a", "Книга А")

	if err := st.AddToShelf(ctx, u1, b1); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Второй пользователь: CreateRootAdmin допускает лишь одного root,
	// поэтому вставляем обычного читателя напрямую через *DBStore.
	db := st.(*DBStore)
	var u2 int64
	if err := db.db.QueryRowContext(ctx,
		db.bind("INSERT INTO users (username, password_hash, role) VALUES (%s, %s, 'reader') RETURNING id", 2),
		"u2", "hash").Scan(&u2); err != nil {
		t.Fatalf("create u2: %v", err)
	}
	pg, err := st.ShelfBooks(ctx, u2, 1, 10)
	if err != nil {
		t.Fatalf("shelf u2: %v", err)
	}
	if pg.Total != 0 {
		t.Fatalf("полка второго пользователя должна быть пуста, получено %d", pg.Total)
	}
}

func TestLatestBooksOrder(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	b1 := importBook(t, st, "/lib/a", "Книга А")
	b2 := importBook(t, st, "/lib/b", "Книга Б")
	b3 := importBook(t, st, "/lib/c", "Книга В")

	pg, err := st.LatestBooks(ctx, 1, 10)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if pg.Total != 3 || len(pg.Items) != 3 {
		t.Fatalf("ожидалось 3 книги, получено total=%d items=%d", pg.Total, len(pg.Items))
	}
	// Сортировка по id DESC: b3, b2, b1.
	want := []int64{b3, b2, b1}
	for i, w := range want {
		if pg.Items[i].ID != w {
			t.Fatalf("позиция %d: ожидался id=%d, получено id=%d", i, w, pg.Items[i].ID)
		}
	}
}
