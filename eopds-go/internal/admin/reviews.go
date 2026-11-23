package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// Ограничения отзывов.
const (
	commentMaxLen  = 4000 // максимум символов в отзыве
	commentShowMax = 50   // сколько отзывов показывать на карточке
	ratingStarsMax = 5    // шкала звёзд (в БД — 0..10, шаг половинки)
)

// attachBookReviews добавляет в data агрегат рейтинга и список отзывов книги.
// Для гостя MyScore/CanDelete не заполняются (userID = 0).
func (s *Server) attachBookReviews(r *http.Request, data map[string]any, bookID int64) {
	st := s.getStore()
	if st == nil {
		return
	}
	u := currentUser(r)
	var userID int64
	isMod := false
	if u != nil {
		userID = u.ID
		isMod = isModerator(u)
	}

	sum, err := st.BookRatingSummary(r.Context(), bookID, userID)
	if err != nil {
		s.log.Warn("reviews: rating summary", "book", bookID, "err", err)
	}
	data["Rating"] = sum
	data["StarsMax"] = ratingStarsMax

	if n, err := st.BookDownloadCount(r.Context(), bookID); err != nil {
		s.log.Warn("reviews: download count", "book", bookID, "err", err)
	} else {
		data["DownloadCount"] = n
	}

	comments, err := st.BookComments(r.Context(), bookID, commentShowMax, userID, isMod)
	if err != nil {
		s.log.Warn("reviews: comments", "book", bookID, "err", err)
	}
	data["Comments"] = comments
	data["CommentMaxLen"] = commentMaxLen
}

// isModerator сообщает, может ли пользователь модерировать отзывы (модератор/админ/root).
func isModerator(u *store.User) bool {
	if u == nil {
		return false
	}
	return u.IsRoot || u.Role == "admin" || u.Role == "moderator"
}

// handleBookRate сохраняет оценку пользователя (POST /library/book/{id}/rate).
// Поле score: 1..10 (0..5 звёзд с шагом 0.5). Пустое/битое — ошибка.
func (s *Server) handleBookRate(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := atoi64(r.PathValue("id"))
	bk, err := st.GetBook(r.Context(), bookID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	score, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("score")))
	if err != nil || score < 0 || score > 10 {
		s.flash(w, r, "book", "err", "reviews.err_score")
		s.redirect(w, r, bookPath(bookID))
		return
	}
	if err := st.SetBookRating(r.Context(), bookID, u.ID, score); err != nil {
		s.log.Warn("reviews: set rating", "book", bookID, "err", err)
		s.flash(w, r, "book", "err", "reviews.err_save")
		s.redirect(w, r, bookPath(bookID))
		return
	}
	s.logEventUser(r.Context(), u, store.EventInfo, "book.rate",
		map[string]string{"book": bk.Title, "stars": formatStars(score), "_book_id": strconv.FormatInt(bookID, 10)})
	s.flash(w, r, "book", "ok", "reviews.ok_rated")
	s.redirect(w, r, bookPath(bookID))
}

// handleBookCommentAdd добавляет отзыв (POST /library/book/{id}/comment).
func (s *Server) handleBookCommentAdd(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := atoi64(r.PathValue("id"))
	bk, err := st.GetBook(r.Context(), bookID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	body := strings.TrimSpace(r.PostFormValue("body"))
	if body == "" {
		s.flash(w, r, "book", "err", "reviews.err_empty")
		s.redirect(w, r, bookPath(bookID))
		return
	}
	if rs := []rune(body); len(rs) > commentMaxLen {
		body = string(rs[:commentMaxLen])
	}
	if _, err := st.AddBookComment(r.Context(), bookID, u.ID, body); err != nil {
		s.log.Warn("reviews: add comment", "book", bookID, "err", err)
		s.flash(w, r, "book", "err", "reviews.err_save")
		s.redirect(w, r, bookPath(bookID))
		return
	}
	s.logEventUser(r.Context(), u, store.EventInfo, "book.comment",
		map[string]string{"book": bk.Title, "_book_id": strconv.FormatInt(bookID, 10)})
	s.flash(w, r, "book", "ok", "reviews.ok_added")
	s.redirect(w, r, bookPath(bookID))
}

// handleBookCommentDelete удаляет отзыв (POST /library/book/{id}/comment/{cid}/delete).
// Свой отзыв — автор; любой — модератор/админ.
func (s *Server) handleBookCommentDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	st := s.getStore()
	if u == nil || st == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := atoi64(r.PathValue("id"))
	cid := atoi64(r.PathValue("cid"))
	bk, _ := st.GetBook(r.Context(), bookID)
	mod := isModerator(u)
	if err := st.DeleteBookComment(r.Context(), cid, u.ID, mod); err != nil {
		s.flash(w, r, "book", "err", "reviews.err_delete")
		s.redirect(w, r, bookPath(bookID))
		return
	}
	args := map[string]string{"_book_id": strconv.FormatInt(bookID, 10)}
	if bk.Title != "" {
		args["book"] = bk.Title
	}
	s.logEventUser(r.Context(), u, store.EventWarn, "book.comment_delete", args)
	s.flash(w, r, "book", "ok", "reviews.ok_deleted")
	s.redirect(w, r, bookPath(bookID))
}

// formatStars переводит оценку 0..10 в строку «X.X» звёзд (например, 7 → «3.5»).
func formatStars(score int) string {
	return strconv.FormatFloat(float64(score)/2.0, 'f', 1, 64)
}

// bookPath — путь карточки книги.
func bookPath(id int64) string {
	return "/library/book/" + strconv.FormatInt(id, 10)
}
