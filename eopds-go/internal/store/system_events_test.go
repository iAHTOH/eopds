package store

import (
	"context"
	"testing"
)

func TestSystemEventsOrderAndRotate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Добавляем 5 событий с монотонным ts, ёмкость — 3.
	for i := 1; i <= 5; i++ {
		err := st.AddSystemEvent(ctx, SystemEvent{
			TS:        int64(i),
			Level:     EventInfo,
			Category:  "test.event",
			Actor:     "u",
			ActorRole: "reader",
			Args:      map[string]string{"n": itoa(i)},
		}, 3)
		if err != nil {
			t.Fatalf("add #%d: %v", i, err)
		}
	}

	n, err := st.CountSystemEvents(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("после ротации ожидалось 3 события, получено %d", n)
	}

	evs, err := st.SystemEvents(ctx, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("len = %d, want 3", len(evs))
	}
	// Новые сверху: ts 5,4,3.
	for i, want := range []int64{5, 4, 3} {
		if evs[i].TS != want {
			t.Fatalf("evs[%d].TS = %d, want %d", i, evs[i].TS, want)
		}
	}
	// args разворачиваются обратно.
	if evs[0].Args["n"] != "5" {
		t.Fatalf("args n = %q, want 5", evs[0].Args["n"])
	}
	if evs[0].ActorRole != "reader" {
		t.Fatalf("actor_role = %q", evs[0].ActorRole)
	}
}

func TestSystemEventsClear(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.AddSystemEvent(ctx, SystemEvent{TS: 1, Category: "x"}, 0); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := st.ClearSystemEvents(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	n, _ := st.CountSystemEvents(ctx)
	if n != 0 {
		t.Fatalf("после очистки count = %d, want 0", n)
	}
}

func TestAddDownload(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	uid, err := st.CreateRootAdmin(ctx, "u1", "hash", "")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	bid := importBook(t, st, "/lib/a", "Книга А")
	if err := st.AddDownload(ctx, bid, uid, "epub"); err != nil {
		t.Fatalf("add download: %v", err)
	}
	// Анонимное скачивание (uid=0).
	if err := st.AddDownload(ctx, bid, 0, "fb2"); err != nil {
		t.Fatalf("add anon download: %v", err)
	}

	// Повторы того же пользователя (например, чтение после скачивания) не
	// создают дубль: уникальный индекс (book_id, user_id). Популярность не растёт.
	before, err := st.BookDownloadCount(ctx, bid)
	if err != nil {
		t.Fatalf("count before: %v", err)
	}
	if err := st.AddDownload(ctx, bid, uid, "read"); err != nil {
		t.Fatalf("repeat download: %v", err)
	}
	after, err := st.BookDownloadCount(ctx, bid)
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("повтор увеличил популярность: было %d, стало %d", before, after)
	}

	// BookDownloadCount: 1 уникальный пользователь + 1 аноним = 2.
	if before != 2 {
		t.Fatalf("BookDownloadCount = %d, want 2", before)
	}
}

// itoa — локальный хелпер для тестов (strconv для наглядности аргументов).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
