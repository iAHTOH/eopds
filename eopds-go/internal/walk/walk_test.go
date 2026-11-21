package walk

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iAHTOH/eopds/eopds-go/internal/emit"
	"github.com/iAHTOH/eopds/eopds-go/internal/logx"
	"github.com/iAHTOH/eopds/eopds-go/internal/meta"
)

const sampleFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description>
<title-info><book-title>Тестовая книга</book-title></title-info>
</description>
</FictionBook>`

// runOnce прогоняет walk по root (без zip) и возвращает собранные записи.
func runOnce(t *testing.T, root string, known map[string]meta.Fingerprint, full bool) []meta.Record {
	t.Helper()
	return runOnceZ(t, root, known, full)
}

// runOnceZ — как runOnce, но с включённым сканированием ZIP.
func runOnceZ(t *testing.T, root string, known map[string]meta.Fingerprint, full bool) []meta.Record {
	t.Helper()
	var buf bytes.Buffer
	em := emit.New(&buf)
	runDone := make(chan struct{})
	go func() {
		Run(Options{
			Root:       root,
			Workers:    2,
			Zip:        true,
			Exts:       []string{".fb2"},
			Logger:     logx.New(logx.LevelError),
			Emitter:    em,
			Known:      known,
			FullRescan: full,
		})
		_ = em.Flush()
		close(runDone)
	}()
	<-runDone

	var recs []meta.Record
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r meta.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("decode record: %v (line=%q)", err, line)
		}
		recs = append(recs, r)
	}
	return recs
}

// countBooks считает записи KindBook.
func countBooks(recs []meta.Record) int {
	n := 0
	for _, r := range recs {
		if r.Kind == meta.KindBook {
			n++
		}
	}
	return n
}

// skippedOf достаёт Skipped из записи KindStats.
func skippedOf(recs []meta.Record) int64 {
	for _, r := range recs {
		if r.Kind == meta.KindStats {
			return r.Skipped
		}
	}
	return -1
}

func TestWalkIncrementalSkipsUnchanged(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "author")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	fp := filepath.Join(sub, "book.fb2")
	if err := os.WriteFile(fp, []byte(sampleFB2), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(fp)
	if err != nil {
		t.Fatal(err)
	}

	// Первый прогон — без Known: книга найдена, ничего не пропущено.
	recs := runOnce(t, root, nil, false)
	if got := countBooks(recs); got != 1 {
		t.Fatalf("первый прогон: ожидалась 1 книга, получено %d", got)
	}
	if sk := skippedOf(recs); sk != 0 {
		t.Fatalf("первый прогон: skipped=%d, ожидалось 0", sk)
	}

	// Запись KindBook должна содержать MTime для отпечатка.
	var bookMTime int64
	for _, r := range recs {
		if r.Kind == meta.KindBook {
			bookMTime = r.MTime
		}
	}
	if bookMTime == 0 {
		t.Fatalf("KindBook.MTime не заполнен")
	}

	// Второй прогон с совпадающим Known — книга пропущена, книг не импортируем.
	known := map[string]meta.Fingerprint{
		"author/book.fb2": {Size: info.Size(), MTime: info.ModTime().UnixNano()},
	}
	recs2 := runOnce(t, root, known, false)
	if got := countBooks(recs2); got != 0 {
		t.Fatalf("второй прогон: ожидалось 0 книг (пропуск), получено %d", got)
	}
	if sk := skippedOf(recs2); sk != 1 {
		t.Fatalf("второй прогон: skipped=%d, ожидалось 1", sk)
	}

	// FullRescan=true игнорирует Known — книга снова читается.
	recs3 := runOnce(t, root, known, true)
	if got := countBooks(recs3); got != 1 {
		t.Fatalf("FullRescan: ожидалась 1 книга, получено %d", got)
	}
}

func TestWalkZipArchiveFingerprint(t *testing.T) {
	root := t.TempDir()
	// Создаём zip с одним FB2 внутри.
	zipPath := filepath.Join(root, "coll.zip")
	if err := writeZip(t, zipPath, "book.fb2", sampleFB2); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// Первый прогон: книги импортируются, есть KindArchiveEnd с Size/MTime архива.
	recs := runOnceZ(t, root, nil, false)
	if got := countBooks(recs); got != 1 {
		t.Fatalf("первый прогон: ожидалась 1 книга из zip, получено %d", got)
	}
	var archSize, archMTime int64
	for _, r := range recs {
		if r.Kind == meta.KindArchiveEnd {
			archSize, archMTime = r.Size, r.MTime
		}
	}
	if archSize != info.Size() || archMTime != info.ModTime().UnixNano() {
		t.Fatalf("archive_end Size/MTime не совпали: got(%d,%d) want(%d,%d)",
			archSize, archMTime, info.Size(), info.ModTime().UnixNano())
	}

	// Второй прогон с Known для архива — весь zip пропущен.
	known := map[string]meta.Fingerprint{
		"coll.zip": {Size: info.Size(), MTime: info.ModTime().UnixNano()},
	}
	recs2 := runOnceZ(t, root, known, false)
	if got := countBooks(recs2); got != 0 {
		t.Fatalf("второй прогон: zip должен быть пропущен, получено книг %d", got)
	}
	if sk := skippedOf(recs2); sk != 1 {
		t.Fatalf("второй прогон: skipped=%d, ожидалось 1 (архив)", sk)
	}
}

// writeZip создаёт zip-архив с одним файлом (для тестов).
func writeZip(t *testing.T, zipPath, name, content string) error {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(content)); err != nil {
		return err
	}
	return zw.Close()
}
