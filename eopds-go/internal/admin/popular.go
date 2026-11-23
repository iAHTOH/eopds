package admin

import (
	"net/http"
	"strings"

	"github.com/iAHTOH/eopds/eopds-go/internal/store"
)

// popularKind нормализует критерий популярности из query (?by=downloads|rating).
func popularKind(r *http.Request) string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("by"))) {
	case store.PopularByRating:
		return store.PopularByRating
	default:
		return store.PopularByDownloads
	}
}

// handleDashboardPopular — страница «Популярные книги»: по скачиваниям или по
// рейтингу, постранично, с сортировкой (по умолчанию — уменьшение рейтинга/
// числа скачиваний). Доступна всем авторизованным (как дашборд).
// GET /dashboard/popular?by=downloads|rating&page=N&sort=...
func (s *Server) handleDashboardPopular(w http.ResponseWriter, r *http.Request) {
	st := s.getStore()
	if st == nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	kind := popularKind(r)
	sort := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))
	if sort == "" {
		sort = "desc" // по умолчанию — уменьшение (рейтинг/скачивания)
	}
	page := pageParam(r)

	pg, err := st.PopularBooks(r.Context(), kind, page, libPageSize, sort == "asc")
	if err != nil {
		s.log.Error("dashboard: popular", "kind", kind, "err", err)
	}

	data := s.libData(r, "books")
	data["ShowList"] = true
	data["Page"] = pg
	data["Popular"] = true
	data["PopularKind"] = kind
	data["Sort"] = sort
	data["Breadcrumbs"] = []Crumb{{Label: "dash.popular"}}
	data["Pagination"] = buildPagination(r.URL.Path, r, pg.Page, pg.Pages)
	s.render(w, r, "library_books", data)
}
