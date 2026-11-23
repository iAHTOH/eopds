package admin

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/fb2render"
)

// errTooLarge — исходный FB2 превысил допустимый размер для рендера.
var errTooLarge = errors.New("reader: source too large")

// maxReaderSource — предел размера исходного FB2, который готовы отрендерить
// целиком в память. 64 МиБ с запасом покрывает реальные книги коллекции
// (включая тексты с base64-иллюстрациями).
const maxReaderSource = 64 << 20

// handleBookRead отдаёт страницу онлайн-читалки книги.
func (s *Server) handleBookRead(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	id := atoi64(r.PathValue("id"))
	book, err := st.GetBook(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Читалка поддерживает только FB2 (для прочих форматов — скачивание).
	if !strings.EqualFold(book.Format, "fb2") {
		http.Redirect(w, r, "/library/book/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}

	data := s.libData(r, "books")
	data["Book"] = book

	// Открытие читалки кладёт книгу на полку пользователя (как и скачивание)
	// и учитывается в популярности (downloads, format='read', уникально на пользователя).
	if u := currentUser(r); u != nil {
		if err := st.AddToShelf(r.Context(), u.ID, id); err != nil {
			s.log.Error("reader: add to shelf", "id", id, "err", err)
		}
	}
	s.recordRead(r, id)

	// Сохранённый прогресс чтения (последняя позиция).
	pos := ""
	if u := currentUser(r); u != nil {
		if p, err := st.ReadingProgress(r.Context(), u.ID, id); err == nil {
			pos = p
		}
	}
	data["StartPercent"] = parsePercent(pos)

	s.render(w, r, "reader", data)
}

// handleBookHTML отдаёт отрендеренное тело книги как HTML-фрагмент.
// Картинки встраиваются как data URI, поэтому фрагмент самодостаточен.
func (s *Server) handleBookHTML(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.NotFound(w, r)
		return
	}
	id := atoi64(r.PathValue("id"))
	src, err := st.BookFile(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer src.Reader.Close()

	body, err := readLimit(src.Reader, maxReaderSource)
	if err != nil {
		s.log.Error("reader: read source", "id", id, "err", err)
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	// Картинки встраиваются inline (data URI) — работаем без отдельного
	// эндпоинта и без зависимости от путей.
	res, err := fb2render.Render(strings.NewReader(body), fb2render.Options{WithNotes: true})
	if err != nil {
		s.log.Error("reader: render", "id", id, "err", err)
		http.Error(w, "render error", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	_, _ = w.Write([]byte(res.HTML))
}

// handleBookProgress сохраняет позицию чтения книги (bookmarks.position).
// Принимает form value "position" (например, "percent:12.5") или query.
func (s *Server) handleBookProgress(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil || currentUser(r) == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := atoi64(r.PathValue("id"))
	pos := strings.TrimSpace(r.FormValue("position"))
	if pos == "" {
		pos = strings.TrimSpace(r.URL.Query().Get("position"))
	}
	if len(pos) > 64 {
		pos = pos[:64]
	}
	u := currentUser(r)
	if err := st.SaveReadingProgress(r.Context(), u.ID, id, pos); err != nil {
		s.log.Error("reader: save progress", "id", id, "err", err)
		http.Error(w, "save error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parsePercent извлекает долю прокрутки из строки позиции "percent:12.5".
// Возвращает 0 при отсутствии/ошибке.
func parsePercent(pos string) float64 {
	pos = strings.TrimSpace(pos)
	if !strings.HasPrefix(pos, "percent:") {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimPrefix(pos, "percent:"), 64)
	if err != nil || v < 0 || v > 100 {
		return 0
	}
	return v
}

// readLimit читает из r не более limit байт; при превышении — errTooLarge.
func readLimit(r io.Reader, limit int64) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > limit {
		return "", errTooLarge
	}
	return string(b), nil
}
