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
	"time"

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

	// OnProgress вызывается после обработки каждой книги (для UI-счётчика).
	OnProgress func(Progress)
}

// Progress — снимок прогресса сканирования (для отображения в UI).
type Progress struct {
	Found    int64
	Parsed   int64
	Created  int64
	Updated  int64
	Errors   int64
	Archives int64
}

// Result — итог сканирования.
type Result struct {
	Roots        []string
	BooksFound   int64
	BooksParsed  int64
	BooksCreated int64
	BooksUpdated int64
	ImportErrors int64
	StartedAt    time.Time     // когда началось сканирование
	Elapsed      time.Duration // общая длительность
}

// maxLoggedImportErrors — сколько первых ошибок импорта писать в лог.
const maxLoggedImportErrors = 20

// Run сканирует каталоги roots и пишет результат в БД.
// Возвращает статистику; ошибка — только если не удалось настроить поток.
// Отмена ctx останавливает цикл без «ложных» ошибок импорта.
func Run(ctx context.Context, st store.Store, opts Options) (Result, error) {
	res := Result{Roots: opts.Roots, StartedAt: time.Now()}
	if len(opts.Roots) == 0 {
		return res, nil
	}

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	var importErr error
	var mu sync.Mutex
	logged := 0

	emitProgress := func() {
		if opts.OnProgress == nil {
			return
		}
		mu.Lock()
		p := Progress{
			Found:   res.BooksFound,
			Parsed:  res.BooksParsed,
			Created: res.BooksCreated,
			Updated: res.BooksUpdated,
			Errors:  res.ImportErrors,
		}
		mu.Unlock()
		opts.OnProgress(p)
	}

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
					handleRecord(ctx, st, rec, &res, &mu, &importErr, opts.Logger, &logged)
					emitProgress()
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

	// Убрать каталоги, пропавшие с диска (без книг и без детей).
	// Только если скан не был прерван — иначе prune удалит «ещё не дойденные».
	if ctx.Err() == nil {
		if _, err := st.PruneCatalogs(ctx); err != nil {
			opts.Logger.Warnf("scansvc", "prune catalogs: %v", err)
		}
	}

	res.Elapsed = time.Since(res.StartedAt)
	if importErr != nil {
		return res, importErr
	}
	return res, nil
}

// handleRecord обрабатывает одну запись сканера.
func handleRecord(ctx context.Context, st store.Store, rec meta.Record, res *Result, mu *sync.Mutex, importErr *error, log *logx.Logger, logged *int) {
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
			// Отмена контекста — не ошибка импорта, а штатная остановка.
			if ctx.Err() != nil {
				mu.Unlock()
				return
			}
			res.ImportErrors++
			if log != nil && *logged < maxLoggedImportErrors {
				*logged++
				log.Warnf("scansvc", "import error %q: %v", rec.BookPath+"/"+rec.Filename, err)
			}
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
