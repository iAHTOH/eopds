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

	// BatchSize — размер пачки книг для пакетной записи в БД (0 = 500).
	BatchSize int
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

// defaultBatchSize — размер пачки книг для пакетной записи в БД.
const defaultBatchSize = 500

// Run сканирует каталоги roots и пишет результат в БД.
//
// Книги из потока сканера накапливаются в буфер и пишутся пачками
// (store.ImportBooks — одна транзакция на пачку). Это убирает thousands
// BEGIN/COMMIT при первичном сканировании и повторные SELECT справочников
// (авторы/серии/жанры/каталоги кэшируются в пределах пачки).
//
// Отмена ctx останавливает цикл без «ложных» ошибок импорта: недописанная
// пачка сбрасывается, а прерванная — не учитывается в Errors.
func Run(ctx context.Context, st store.Store, opts Options) (Result, error) {
	res := Result{Roots: opts.Roots, StartedAt: time.Now()}
	if len(opts.Roots) == 0 {
		return res, nil
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	pr, pw := io.Pipe()
	var wg sync.WaitGroup
	var importErr error
	var mu sync.Mutex

	// импортёр пишет накопленную пачку и обновляет статистику.
	// Возвращает false, если импорт прерван отменой ctx.
	flush := func(batch []store.IngestBook) bool {
		if len(batch) == 0 {
			return true
		}
		br, err := st.ImportBooks(ctx, batch)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			// Отмена контекста — штатная остановка, не ошибка импорта.
			if ctx.Err() != nil {
				return false
			}
			// Сбой самой транзакции: считаем ошибками всю пачку.
			res.ImportErrors += int64(len(batch))
			if opts.Logger != nil {
				opts.Logger.Warnf("scansvc", "import batch failed (%d books): %v", len(batch), err)
			}
			return true
		}
		res.BooksParsed += br.Created + br.Updated
		res.BooksCreated += br.Created
		res.BooksUpdated += br.Updated
		res.ImportErrors += br.Errors
		return true
	}

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

	loggedErrs := 0

	// Читатель: построчно разбирает JSON, копит книги в пачку и сбрасывает.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := bufio.NewReaderSize(pr, 256*1024)
		batch := make([]store.IngestBook, 0, batchSize)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				var rec meta.Record
				if jerr := json.Unmarshal(bytes.TrimRight(line, "\r\n"), &rec); jerr == nil {
					switch rec.Kind {
					case meta.KindBook:
						mu.Lock()
						res.BooksFound++
						mu.Unlock()
						batch = append(batch, ingestFromRecord(rec))
						if len(batch) >= batchSize {
							if !flush(batch) {
								_ = pr.Close()
								return
							}
							batch = batch[:0]
							emitProgress()
						}
					case meta.KindError:
						mu.Lock()
						res.ImportErrors++
						n := loggedErrs
						loggedErrs++
						mu.Unlock()
						if opts.Logger != nil && n < maxLoggedImportErrors {
							opts.Logger.Warnf("scansvc", "parse error %s/%s: %s", rec.RelPath, rec.Filename, rec.Error)
						}
					case meta.KindStats:
						// Итоговая статистика walk — только для логов; наши
						// счётчики (BooksFound/Parsed/Created/...) уже точны и
						// основаны на реальном импорте, поэтому не перезаписываем.
					}
				}
			}
			if err != nil {
				break
			}
		}
		// Остаток пачки.
		flush(batch)
		emitProgress()
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

// ingestFromRecord превращает запись сканера в IngestBook для store.
func ingestFromRecord(rec meta.Record) store.IngestBook {
	var seriesTitle string
	var seriesIndex int
	if rec.Series != nil {
		seriesTitle = rec.Series.Title
		seriesIndex = rec.Series.Index
	}
	return store.IngestBook{
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
	}
}
