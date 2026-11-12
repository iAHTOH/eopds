// Package emit — потокобезопасная запись записей (Record) в stdout
// в формате JSON Lines.
//
// Один экземпляр Emitter используется всеми воркерами одновременно,
// поэтому внутри есть мьютекс. Каждая запись — одна строка JSON + '\n'.
// Python-адаптер читает stdout построчно.
package emit

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"

	"github.com/iAHTOH/eopds/eopds-go/internal/meta"
)

// Emitter пишет JSON Lines в заданный writer (обычно os.Stdout).
type Emitter struct {
	mu  sync.Mutex
	w   *bufio.Writer
	err error
}

// New создаёт Emitter поверх writer.
func New(w io.Writer) *Emitter {
	return &Emitter{w: bufio.NewWriterSize(w, 64*1024)}
}

// Write сериализует и записывает одну запись. Потокобезопасно.
// Ошибки записи запоминаются: если запись «сломалась» (например, приёмник
// закрылся), дальнейшие записи игнорируются, чтобы не засорять вывод.
func (e *Emitter) Write(r meta.Record) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return
	}
	b, err := json.Marshal(r)
	if err != nil {
		e.err = err
		return
	}
	if _, err := e.w.Write(b); err != nil {
		e.err = err
		return
	}
	if err := e.w.WriteByte('\n'); err != nil {
		e.err = err
	}
}

// Flush сбрасывает буфер. Вызывать перед завершением процесса.
func (e *Emitter) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	return e.w.Flush()
}

// Err возвращает накопленную ошибку записи (если была).
func (e *Emitter) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}
