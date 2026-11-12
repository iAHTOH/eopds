// Package walk реализует параллельный обход файловой системы и ZIP-архивов
// с извлечением метаданных книг через соответствующие парсеры.
//
// Модель параллелизма:
//   - один «производитель» (producer) обходит дерево каталогов и кладёт
//     задачи в канал jobs;
//   - N воркеров (worker) читают задачи, парсят книгу/архив и пишут
//     результат в общий Emitter (он потокобезопасен);
//   - всё это управляется через WaitGroup и закрытие каналов.
//
// Узкие места (чтение файлов, распаковка ZIP, парсинг XML) распределяются
// между воркерами — в этом и есть источник ускорения относительно
// однопоточного Python-сканера.
package walk

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iAHTOH/eopds/eopds-go/internal/emit"
	"github.com/iAHTOH/eopds/eopds-go/internal/fb2"
	"github.com/iAHTOH/eopds/eopds-go/internal/logx"
	"github.com/iAHTOH/eopds/eopds-go/internal/meta"
)

// CatalogType — числовые типы каталогов, синхронизированы с opdsdb.CAT_*.
const (
	CatNormal = 0
	CatZip    = 1
	CatINPX   = 2
)

// Options — параметры сканирования.
type Options struct {
	Root    string   // EOPDS_ROOT_LIB
	Workers int      // число воркеров (горутин-обработчиков)
	Zip     bool     // сканировать ли ZIP-архивы
	Exts    []string // расширения книг (в нижнем регистре, с точкой): ".fb2", ...
	Debug   bool     // подробные логи
	Logger  *logx.Logger
	Emitter *emit.Emitter
}

// job — задача для воркера.
type job struct {
	kind    int    // jobFile | jobZip
	absPath string // абсолютный путь к файлу/архиву
	relDir  string // путь относительно Root (для БД)
	name    string // имя файла (для БД)
	format  string // расширение без точки
	size    int64
}

const (
	jobFile = iota
	jobZip
)

// Stats — счётчики сканирования (атомарные).
type Stats struct {
	booksFound   int64
	booksParsed  int64
	archivesSeen int64
	badBooks     int64
	badArchives  int64
}

// Run выполняет сканирование. Возвращает итоговую статистику.
//
// Статистика также отправляется в поток как Record{Kind: KindStats}.
func Run(opts Options) {
	if opts.Workers < 1 {
		opts.Workers = 1
	}
	log := opts.Logger
	em := opts.Emitter

	log.Infof("walk", "starting scan: root=%s workers=%d zip=%v exts=%v", opts.Root, opts.Workers, opts.Zip, opts.Exts)

	var stats Stats
	extSet := make(map[string]struct{}, len(opts.Exts))
	for _, e := range opts.Exts {
		extSet[strings.ToLower(e)] = struct{}{}
	}

	jobs := make(chan job, opts.Workers*4)
	var wg sync.WaitGroup

	start := time.Now()

	// Запуск воркеров.
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range jobs {
				processJob(j, opts, em, log, &stats, extSet)
			}
		}(i)
	}

	// Производитель: обход дерева.
	produce(opts, jobs, log, &stats, extSet)

	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)

	// Итоговая статистика — в поток данных (для Python-адаптера)
	// и краткая сводка в лог.
	em.Write(meta.Record{
		Kind:         meta.KindStats,
		BooksFound:   atomic.LoadInt64(&stats.booksFound),
		BooksParsed:  atomic.LoadInt64(&stats.booksParsed),
		ArchivesSeen: atomic.LoadInt64(&stats.archivesSeen),
		BadBooks:     atomic.LoadInt64(&stats.badBooks),
		BadArchives:  atomic.LoadInt64(&stats.badArchives),
		ElapsedMS:    float64(elapsed.Milliseconds()),
		Workers:      opts.Workers,
	})
	log.Infof("walk", "scan done: found=%d parsed=%d archives=%d bad=%d/%d elapsed=%s",
		atomic.LoadInt64(&stats.booksFound),
		atomic.LoadInt64(&stats.booksParsed),
		atomic.LoadInt64(&stats.archivesSeen),
		atomic.LoadInt64(&stats.badBooks),
		atomic.LoadInt64(&stats.badArchives),
		elapsed,
	)
}

// produce обходит дерево каталогов и отправляет задачи в jobs.
func produce(opts Options, jobs chan<- job, log *logx.Logger, stats *Stats, extSet map[string]struct{}) {
	root := opts.Root
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			log.Warnf("walk", "error accessing %s: %v", path, err)
			return nil // не прерываем весь обход из-за одного элемента
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		ext := strings.ToLower(filepath.Ext(name))
		relDir, rerr := filepath.Rel(root, filepath.Dir(path))
		if rerr != nil {
			relDir = "."
		}

		if ext == ".zip" {
			if !opts.Zip {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				log.Warnf("walk", "stat error %s: %v", path, ierr)
				return nil
			}
			jobs <- job{
				kind:    jobZip,
				absPath: path,
				relDir:  relDir,
				name:    name,
				size:    info.Size(),
			}
			return nil
		}

		if _, ok := extSet[ext]; !ok {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			log.Warnf("walk", "stat error %s: %v", path, ierr)
			return nil
		}
		jobs <- job{
			kind:    jobFile,
			absPath: path,
			relDir:  relDir,
			name:    name,
			format:  strings.TrimPrefix(ext, "."),
			size:    info.Size(),
		}
		return nil
	})
	if walkErr != nil {
		log.Errorf("walk", "tree walk failed: %v", walkErr)
	}
}

// processJob обрабатывает одну задачу (файл или ZIP).
func processJob(j job, opts Options, em *emit.Emitter, log *logx.Logger, stats *Stats, extSet map[string]struct{}) {
	switch j.kind {
	case jobFile:
		emitFileBook(j, opts, em, log, stats, CatNormal, j.relDir, j.name, j.absPath, j.size)
	case jobZip:
		atomic.AddInt64(&stats.archivesSeen, 1)
		processZip(j, opts, em, log, stats, extSet)
	}
}

// emitFileBook парсит отдельный файл книги и отправляет Record.
func emitFileBook(j job, opts Options, em *emit.Emitter, log *logx.Logger, stats *Stats, catalogType int, relPath, name, absPath string, size int64) {
	atomic.AddInt64(&stats.booksFound, 1)

	format := strings.ToLower(filepath.Ext(name))
	format = strings.TrimPrefix(format, ".")

	data, err := parseBookFile(absPath, format)
	if err != nil {
		atomic.AddInt64(&stats.badBooks, 1)
		log.Warnf("walk", "parse error %s: %v", absPath, err)
		em.Write(meta.Record{
			Kind:     meta.KindError,
			RelPath:  relPath,
			Filename: name,
			Error:    err.Error(),
		})
		return
	}

	atomic.AddInt64(&stats.booksParsed, 1)

	rec := meta.Record{
		Kind:        meta.KindBook,
		CatalogType: catalogType,
		RelPath:     relPath,
		Filename:    name,
		Format:      format,
		Size:        size,
		Title:       data.Title,
		Lang:        data.Lang,
		DocDate:     data.DocDate,
		Annotation:  data.Annotation,
		Authors:     data.Authors,
		Genres:      data.Genres,
		Series:      data.Series,
		BookPath:    relPath,
	}
	em.Write(rec)
	log.Debugf("walk", "book parsed: %s/%s (%s)", relPath, name, format)
}

// processZip открывает архив и парсит входящие в него книги.
func processZip(j job, opts Options, em *emit.Emitter, log *logx.Logger, stats *Stats, extSet map[string]struct{}) {
	// rel_path для книг внутри ZIP — путь к каталогу с архивом.
	archiveRel := filepath.Join(j.relDir, j.name)

	em.Write(meta.Record{Kind: meta.KindArchiveStart, ArchivePath: archiveRel})
	log.Debugf("walk", "archive start: %s", archiveRel)

	zr, err := zip.OpenReader(j.absPath)
	if err != nil {
		atomic.AddInt64(&stats.badArchives, 1)
		log.Warnf("walk", "cannot open zip %s: %v", j.absPath, err)
		em.Write(meta.Record{Kind: meta.KindError, ArchivePath: archiveRel, Error: err.Error()})
		return
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		if _, ok := extSet[ext]; !ok {
			continue
		}
		format := strings.TrimPrefix(ext, ".")

		atomic.AddInt64(&stats.booksFound, 1)

		rc, oerr := f.Open()
		if oerr != nil {
			atomic.AddInt64(&stats.badBooks, 1)
			log.Warnf("walk", "cannot open %s in %s: %v", f.Name, j.absPath, oerr)
			continue
		}

		data, perr := parseBookReader(rc, format)
		rc.Close()
		if perr != nil {
			atomic.AddInt64(&stats.badBooks, 1)
			log.Warnf("walk", "parse error %s in %s: %v", f.Name, j.absPath, perr)
			em.Write(meta.Record{
				Kind:     meta.KindError,
				RelPath:  archiveRel,
				Filename: filepath.Base(f.Name),
				Error:    perr.Error(),
			})
			continue
		}

		atomic.AddInt64(&stats.booksParsed, 1)

		// Для книги из ZIP: rel_path и book_path = путь к архиву.
		// Это совпадает с текущей Python-логикой (opdsdb.addbook получает
		// rel_path архива как book.path), поэтому дедупликация findbook()
		// по (filename, path) работает с уже проиндексированной БД.
		em.Write(meta.Record{
			Kind:        meta.KindBook,
			CatalogType: CatZip,
			RelPath:     archiveRel,
			Filename:    filepath.Base(f.Name),
			Format:      format,
			Size:        int64(f.UncompressedSize64),
			Title:       data.Title,
			Lang:        data.Lang,
			DocDate:     data.DocDate,
			Annotation:  data.Annotation,
			Authors:     data.Authors,
			Genres:      data.Genres,
			Series:      data.Series,
			BookPath:    archiveRel,
		})
	}

	em.Write(meta.Record{Kind: meta.KindArchiveEnd, ArchivePath: archiveRel})
}

// parseBookFile открывает файл и парсит по формату.
func parseBookFile(absPath, format string) (meta.BookData, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return meta.BookData{}, err
	}
	defer f.Close()
	return parseBookReader(f, format)
}

// parseBookReader выбирает парсер по формату (пока только FB2).
func parseBookReader(r io.Reader, format string) (meta.BookData, error) {
	switch format {
	case "fb2":
		return fb2.Parse(r)
	default:
		// На текущем этапе поддерживается только FB2.
		// Остальные форматы будут добавлены позже; возвращаем пустые метаданные,
		// но не считаем это ошибкой парсинга — книга всё равно будет учтена.
		return meta.BookData{}, nil
	}
}
