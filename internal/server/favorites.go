package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/chillincool/tilegate/internal/apps"
	"github.com/chillincool/tilegate/internal/auth"
)

// UserStore is implemented by store.Store.
type UserStore interface {
	Favorites(ctx context.Context, user string) ([]string, error)
	AddFavorite(ctx context.Context, user, app string) error
	RemoveFavorite(ctx context.Context, user, app string) error
	Theme(ctx context.Context, user string) (string, error)
	SetTheme(ctx context.Context, user, theme string) error
	Ping(ctx context.Context) error
}

// tiles pairs the visible apps with the user's favourites. The favourites
// list is built only from visible apps, so a saved favourite for an app the
// user has since lost access to is simply not shown.
func (s *Server) tiles(r *http.Request, u *auth.User, visible []apps.App) (all, favs []tile) {
	var ids []string
	if s.DB != nil {
		var err error
		if ids, err = s.DB.Favorites(r.Context(), u.Subject); err != nil {
			// Still render the page; favourites are a convenience.
			s.Log.Error("loading favourites", "err", err)
		}
	}
	all = make([]tile, len(visible))
	byID := make(map[string]tile, len(visible))
	for i, a := range visible {
		all[i] = tile{App: a, Fav: slices.Contains(ids, a.ID), Stars: s.DB != nil}
		byID[a.ID] = all[i]
	}
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			favs = append(favs, t)
		}
	}
	return all, favs
}

func (s *Server) registerFavorites(mux *http.ServeMux) {
	if s.DB == nil {
		return
	}
	// Rejects cross-site PUT/DELETE (CSRF) using Sec-Fetch-Site and Origin.
	csrf := http.NewCrossOriginProtection()
	mux.HandleFunc("GET /api/favorites", s.listFavorites)
	mux.Handle("PUT /api/favorites/{id}", csrf.Handler(http.HandlerFunc(s.changeFavorite)))
	mux.Handle("DELETE /api/favorites/{id}", csrf.Handler(http.HandlerFunc(s.changeFavorite)))
}

func (s *Server) listFavorites(w http.ResponseWriter, r *http.Request) {
	u := s.user(w, r)
	if u == nil {
		return
	}
	_, favs := s.tiles(r, u, s.Filter.Visible(s.Store.Get(), u.Claims))
	ids := make([]string, 0, len(favs))
	for _, f := range favs {
		ids = append(ids, f.ID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ids)
}

func (s *Server) changeFavorite(w http.ResponseWriter, r *http.Request) {
	u := s.user(w, r)
	if u == nil {
		return
	}
	if u.Subject == "" {
		http.Error(w, "no stable user id to save favourites under", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	visible := s.Filter.Visible(s.Store.Get(), u.Claims)
	if !slices.ContainsFunc(visible, func(a apps.App) bool { return a.ID == id }) {
		http.Error(w, "unknown app", http.StatusNotFound)
		return
	}
	var err error
	if r.Method == http.MethodPut {
		err = s.DB.AddFavorite(r.Context(), u.Subject, id)
	} else {
		err = s.DB.RemoveFavorite(r.Context(), u.Subject, id)
	}
	if err != nil {
		s.Log.Error("saving favourite", "err", err)
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
