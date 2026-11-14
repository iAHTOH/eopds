// Package scansvc связывает готовый сканер (walk) с записью в новую БД.
//
// Сканер пишет поток JSON Lines в emit.Emitter. Здесь он подключается к
// io.Pipe: записи читаются построчно и импортируются через store.ImportBook
// в транзакциях (idempotent по (path, filename)).
package scansvc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/iAHTOH/eopds/eopds-go/internal/emit"
	"github.com/iAHTOH/eopds/eopds-go/internal/logx"
	"github.com/iAHTOH/eopds/eopds-go/internal/meta"
	"github.com/iAHTOH/eopds/eopds-go/internal/store"
	"github.com/iAHTOH/eopds/eopds-go/internal/walk"
)

// Options — параметры запуска сканирования.
type Options struct {
	Roots   []string // каталоги сканирования
	Workers int
	Zip     bool
	Exts    []string
	Logger  *logx.Logger
}

// Result — итог сканирования.
type Result struct {
	Roots        []string
	BooksFound   int64
	BooksParsed  int64
	BooksCreated int64
	BooksUpdated int64
	ImportErrors int64
}

// Run сканирует каталоги roots и пишет результат в БД.
// Возвращает статистику; ошибка — только если не удалось настроить поток.
func Run(ctx context.Context, st store.Store, opts Options) (Result, error) {
	res := Result{Roots: opts.Roots}
	if len(opts.Roots) == 0 {
		return res, nil
	}

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	var importErr error
	var mu sync.Mutex

	// Читатель: построчно разбирает JSON и импортирует книги.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := bufio.NewReaderSize(pr, 256*1024)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				var rec meta.Record
				if jerr := json.Unmarshal(bytes.TrimRight(line, "\r\n"), &rec); jerr == nil {
					handleRecord(ctx, st, rec, &res, &mu, &importErr)
				}
			}
			if err != nil {
				break
			}
		}
		_ = pr.Close()
	}()

	// Писатель: сканер шлёт записи в pipe.
	for _, root := range opts.Roots {
		if ctx.Err() != nil {
			break
		}
		em := emit.New(pw)
		rootOpts := walk.Options{
			Root:    root,
			Workers: opts.Workers,
			Zip:     opts.Zip,
			Exts:    opts.Exts,
			Logger:  opts.Logger,
			Emitter: em,
		}
		func() {
			defer func() { _ = em.Flush() }()
			defer func() {
				// не даём панике воркеров уронить процесс
				if rec := recover(); rec != nil {
					mu.Lock()
					importErr = fmt.Errorf("scansvc: panic in walk: %v", rec)
					mu.Unlock()
				}
			}()
			walk.Run(rootOpts)
		}()
	}
	_ = pw.Close()
	wg.Wait()

	if importErr != nil {
		return res, importErr
	}
	return res, nil
}

// handleRecord обрабатывает одну запись сканера.
func handleRecord(ctx context.Context, st store.Store, rec meta.Record, res *Result, mu *sync.Mutex, importErr *error) {
	switch rec.Kind {
	case meta.KindBook:
		mu.Lock()
		res.BooksFound++
		mu.Unlock()

		var seriesTitle string
		var seriesIndex int
		if rec.Series != nil {
			seriesTitle = rec.Series.Title
			seriesIndex = rec.Series.Index
		}
		imported, err := st.ImportBook(ctx, store.IngestBook{
			CatalogType: rec.CatalogType,
			RelPath:     rec.RelPath,
			BookPath:    rec.BookPath,
			Filename:    rec.Filename,
			Format:      rec.Format,
			Size:        rec.Size,
			Title:       rec.Title,
			Lang:        rec.Lang,
			DocDate:     rec.DocDate,
			Annotation:  rec.Annotation,
			Authors:     rec.Authors,
			Genres:      rec.Genres,
			SeriesTitle: seriesTitle,
			SeriesIndex: seriesIndex,
		})
		mu.Lock()
		if err != nil {
			res.ImportErrors++
		} else {
			res.BooksParsed++
			if imported.Created {
				res.BooksCreated++
			} else {
				res.BooksUpdated++
			}
		}
		mu.Unlock()
	case meta.KindStats:
		mu.Lock()
		res.BooksFound = rec.BooksFound
		res.BooksParsed = rec.BooksParsed
		mu.Unlock()
	}
}
