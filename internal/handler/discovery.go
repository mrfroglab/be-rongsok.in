package handler

import (
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

func parseFinite(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// GET /discovery/search?lat=&lng=&categoryId=&radius= — pengepul terdekat (publik).
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("lat") == "" || q.Get("lng") == "" {
		httpx.Error(w, http.StatusBadRequest, "lat and lng are required")
		return
	}
	lat, okLat := parseFinite(q.Get("lat"))
	lng, okLng := parseFinite(q.Get("lng"))
	if !okLat || !okLng || math.Abs(lat) > 90 || math.Abs(lng) > 180 {
		httpx.Error(w, http.StatusBadRequest, "lat and lng must be valid coordinates")
		return
	}
	radius := 5.0
	if s := q.Get("radius"); s != "" {
		f, ok := parseFinite(s)
		if !ok || f <= 0 {
			httpx.Error(w, http.StatusBadRequest, "radius must be a positive number")
			return
		}
		radius = math.Min(f, 20000) // > keliling bumi tak ada gunanya
	}
	res, err := store.SearchCollectors(r.Context(), h.DB, lat, lng, radius, q.Get("categoryId"))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, res)
}

// GET /discovery/categories — flat list (FE membangun tree-nya).
func (h *Handler) Categories(w http.ResponseWriter, r *http.Request) {
	cats, err := store.ListCategories(r.Context(), h.DB)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, cats)
}

// GET /discovery/collectors/{id} — detail lapak (publik).
func (h *Handler) CollectorByID(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := store.GetProfileByID(ctx, h.DB, chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if p == nil {
		httpx.Error(w, http.StatusNotFound, "Pengepul tidak ditemukan")
		return
	}
	owner, err := store.GetCollectorOwner(ctx, h.DB, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	catalogs, err := store.CatalogsWithCategory(ctx, h.DB, p.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, struct {
		*store.CollectorProfile
		User     *store.CollectorOwner `json:"user"`
		Catalogs []store.Catalog       `json:"catalogs"`
	}{p, owner, catalogs})
}

// GET /discovery/stats — statistik publik landing page.
func (h *Handler) PublicStats(w http.ResponseWriter, r *http.Request) {
	s, err := store.GetPublicStats(r.Context(), h.DB)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, s)
}

// GET /discovery/leaderboard?userId= — top 20 customer berdasarkan kg terdaur
// ulang; userId opsional → sertakan peringkat user tsb (meski di luar top 20).
func (h *Handler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	board, err := store.Leaderboard(ctx, h.DB, 20)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var me *store.MyRank
	if userID := r.URL.Query().Get("userId"); userID != "" {
		if me, err = store.LeaderboardRank(ctx, h.DB, userID); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	httpx.OK(w, http.StatusOK, map[string]any{"leaderboard": board, "me": me})
}
