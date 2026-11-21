package store

import (
	"context"
	"testing"
)

func TestScanFilesRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Пусто изначально.
	got, err := st.ScanFiles(ctx)
	if err != nil {
		t.Fatalf("scan files (empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ожидался пустой набор, получено %d", len(got))
	}

	// Сохраняем пачку.
	files := map[string]FileFingerprint{
		"a/b.fb2": {Size: 100, MTime: 111},
		"a/c.zip": {Size: 200, MTime: 222},
	}
	if err := st.SaveScanFiles(ctx, files); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err = st.ScanFiles(ctx)
	if err != nil {
		t.Fatalf("scan files: %v", err)
	}
	if len(got) != 2 || got["a/b.fb2"] != (FileFingerprint{Size: 100, MTime: 111}) {
		t.Fatalf("не совпало: %+v", got)
	}

	// Upsert: обновляем существующую запись.
	if err := st.SaveScanFiles(ctx, map[string]FileFingerprint{
		"a/b.fb2": {Size: 150, MTime: 333},
	}); err != nil {
		t.Fatalf("save update: %v", err)
	}
	got, _ = st.ScanFiles(ctx)
	if got["a/b.fb2"] != (FileFingerprint{Size: 150, MTime: 333}) {
		t.Fatalf("обновление не применилось: %+v", got["a/b.fb2"])
	}
	if len(got) != 2 {
		t.Fatalf("upsert не должен добавлять дубли: %d", len(got))
	}

	// ClearScanFiles.
	if err := st.ClearScanFiles(ctx); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = st.ScanFiles(ctx)
	if len(got) != 0 {
		t.Fatalf("после clear ожидалось пусто, получено %d", len(got))
	}
}

func TestClearLibraryClearsScanFiles(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.SaveScanFiles(ctx, map[string]FileFingerprint{"x/y.fb2": {Size: 1, MTime: 1}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := st.ClearLibrary(ctx); err != nil {
		t.Fatalf("clear library: %v", err)
	}
	if got, _ := st.ScanFiles(ctx); len(got) != 0 {
		t.Fatalf("ClearLibrary должна чистить scan_files, осталось %d", len(got))
	}
}
