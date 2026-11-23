package store

import (
	"context"
	"testing"
)

// createUser — вспомогательная: создаёт обычного читателя (не root) и возвращает id.
func createUser(t *testing.T, st Store, username string) int64 {
	t.Helper()
	db := st.(*DBStore)
	var id int64
	if err := db.db.QueryRowContext(context.Background(),
		db.bind("INSERT INTO users (username, password_hash, role) VALUES (%s, %s, 'reader') RETURNING id", 2),
		username, "hash").Scan(&id); err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return id
}

func TestBookRating(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	b := importBook(t, st, "/lib/a", "Книга А")
	u1 := createUser(t, st, "u1")
	u2 := createUser(t, st, "u2")

	// Нет оценок.
	sum, err := st.BookRatingSummary(ctx, b, 0)
	if err != nil {
		t.Fatalf("summary empty: %v", err)
	}
	if sum.Count != 0 || sum.Avg != 0 {
		t.Fatalf("ожидался пустой рейтинг, получено %+v", sum)
	}

	// Две оценки: 6 и 10 → среднее (6+10)/2 = 8 → 4.0 звезды.
	if err := st.SetBookRating(ctx, b, u1, 6); err != nil {
		t.Fatalf("rate u1: %v", err)
	}
	if err := st.SetBookRating(ctx, b, u2, 10); err != nil {
		t.Fatalf("rate u2: %v", err)
	}
	sum, err = st.BookRatingSummary(ctx, b, u1)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.Count != 2 {
		t.Fatalf("ожидалось 2 оценки, получено %d", sum.Count)
	}
	if sum.Avg != 4.0 {
		t.Fatalf("ожидалось среднее 4.0, получено %.2f", sum.Avg)
	}
	if sum.MyScore != 6 {
		t.Fatalf("ожидалась моя оценка 6, получено %d", sum.MyScore)
	}

	// Повторная оценка того же пользователя перезаписывает.
	if err := st.SetBookRating(ctx, b, u1, 10); err != nil {
		t.Fatalf("re-rate u1: %v", err)
	}
	sum, _ = st.BookRatingSummary(ctx, b, u1)
	if sum.Count != 2 {
		t.Fatalf("после переоценки число оценок должно остаться 2, получено %d", sum.Count)
	}
	if sum.MyScore != 10 {
		t.Fatalf("ожидалась моя оценка 10, получено %d", sum.MyScore)
	}
}

func TestBookComments(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	b := importBook(t, st, "/lib/a", "Книга А")
	u1 := createUser(t, st, "u1")
	u2 := createUser(t, st, "u2")

	id1, err := st.AddBookComment(ctx, b, u1, "  Первый отзыв  ")
	if err != nil {
		t.Fatalf("add comment u1: %v", err)
	}
	if _, err := st.AddBookComment(ctx, b, u2, "Второй отзыв"); err != nil {
		t.Fatalf("add comment u2: %v", err)
	}

	list, err := st.BookComments(ctx, b, 10, u1, false)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ожидалось 2 отзыва, получено %d", len(list))
	}
	// Новые сверху: последний добавленный (u2) — первый.
	if list[0].UserName == "" {
		t.Fatalf("у отзыва пустое имя автора")
	}

	// Флаг CanDelete: u1 может удалить только свой отзыв.
	var mine *BookComment
	for i := range list {
		if list[i].UserID == u1 {
			mine = &list[i]
		}
	}
	if mine == nil {
		t.Fatalf("отзыв u1 не найден")
	}
	if !mine.CanDelete {
		t.Fatalf("свой отзыв должен быть удаляемым")
	}
	if mine.Body != "Первый отзыв" {
		t.Fatalf("текст отзыва не обрезан по краям: %q", mine.Body)
	}

	// u2 не может удалить чужой отзыв без прав модератора.
	if err := st.DeleteBookComment(ctx, id1, u2, false); err != ErrNotFound {
		t.Fatalf("ожидался ErrNotFound при удалении чужого отзыва, получено %v", err)
	}
	// Модератор может.
	if err := st.DeleteBookComment(ctx, id1, u2, true); err != nil {
		t.Fatalf("модератор должен удалить чужой отзыв: %v", err)
	}
	list, _ = st.BookComments(ctx, b, 10, 0, false)
	if len(list) != 1 {
		t.Fatalf("после удаления ожидался 1 отзыв, получено %d", len(list))
	}
}
