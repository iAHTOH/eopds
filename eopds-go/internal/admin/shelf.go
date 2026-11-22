package admin

import (
	"net/http"
	"strings"
)

// handleLibraryNew — «Новые поступления»: книги, отсортированные по id DESC
// (последние добавленные сверху), постранично.
func (s *Server) handleLibraryNew(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	page := pageParam(r)
	pg, err := st.LatestBooks(r.Context(), page, libPageSize)
	if err != nil {
		s.log.Error("library: new arrivals", "err", err)
	}
	data := s.libData(r, "books")
	data["ShowList"] = true
	data["Page"] = pg
	data["Breadcrumbs"] = []Crumb{{Label: "lib.new_arrivals"}}
	data["Pagination"] = buildPagination(r.URL.Path, r, pg.Page, pg.Pages)
	s.render(w, r, "library_books", data)
}

// handleLibraryShelf — «Книжная полка» пользователя (книги, добавленные
// скачиванием или открытием читалки), последние сверху, постранично.
func (s *Server) handleLibraryShelf(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	page := pageParam(r)
	pg, err := st.ShelfBooks(r.Context(), u.ID, page, libPageSize)
	if err != nil {
		s.log.Error("library: shelf", "err", err)
	}
	data := s.libData(r, "books")
	data["ShowList"] = true
	data["Page"] = pg
	data["ShelfMode"] = true // шаблон показывает кнопки «удалить»
	data["Breadcrumbs"] = []Crumb{{Label: "lib.shelf"}}
	data["Pagination"] = buildPagination(r.URL.Path, r, pg.Page, pg.Pages)
	s.render(w, r, "library_books", data)
}

// handleShelfRemove — убирает книгу с полки пользователя (POST, форма).
// После — редирект назад на полку (303).
func (s *Server) handleShelfRemove(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	u := currentUser(r)
	if st == nil || u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id := atoi64(r.PathValue("id"))
	if err := st.RemoveFromShelf(r.Context(), u.ID, id); err != nil {
		s.log.Error("library: remove from shelf", "id", id, "err", err)
	}
	// Возврат на ту же страницу полки (с учётом пагинации).
	back := "/library/shelf"
	if q := strings.TrimSpace(r.URL.Query().Get("page")); q != "" {
		back += "?page=" + q
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
