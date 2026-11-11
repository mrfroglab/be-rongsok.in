package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

// POST/PATCH /collector/profile — buat atau perbarui profil lapak.
func (h *Handler) UpsertCollectorProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ShopName       *string
		Description    *string
		RadiusKm       *float64
		IsOpen         *bool
		ShopImageURL   *string
		OperatingHours *string
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	if httpx.Required(v, "shopName", in.ShopName) {
		v.StrLen("shopName", in.ShopName, 3, -1)
	}
	// Pengepul tak lagi punya radius layanan — kolomnya masih ada demi data lama.
	v.Range("radiusKm", in.RadiusKm, 1, 50)
	v.StrLen("operatingHours", in.OperatingHours, -1, 200)
	if v.Fail(w) {
		return
	}
	user := middleware.User(r)
	if user.Role != store.RoleCollector {
		httpx.Error(w, http.StatusForbidden, "Only collectors can manage profiles")
		return
	}
	p, err := store.UpsertProfile(r.Context(), h.DB, user.ID, store.ProfileInput{
		ShopName: *in.ShopName, Description: in.Description, RadiusKm: in.RadiusKm, IsOpen: in.IsOpen,
		ShopImageURL: in.ShopImageURL, OperatingHours: in.OperatingHours,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, p)
}

// PATCH /collector/catalogs — ganti seluruh katalog harga lapak.
func (h *Handler) UpdateCatalogs(w http.ResponseWriter, r *http.Request) {
	var in []struct {
		CategoryID *string
		MinPrice   *float64
		MaxPrice   *float64
		IsActive   *bool
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	if in == nil {
		v.Add("", "Expected array")
	}
	items := make([]store.CatalogInput, 0, len(in))
	for _, c := range in {
		if c.CategoryID == nil || c.MinPrice == nil || c.MaxPrice == nil {
			v.Add("", "categoryId, minPrice & maxPrice wajib diisi")
			continue
		}
		active := true
		if c.IsActive != nil {
			active = *c.IsActive
		}
		items = append(items, store.CatalogInput{CategoryID: *c.CategoryID, MinPrice: *c.MinPrice, MaxPrice: *c.MaxPrice, IsActive: active})
	}
	if v.Fail(w) {
		return
	}

	ctx := r.Context()
	profile, err := store.GetProfileByUser(ctx, h.DB, middleware.User(r).ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if profile == nil {
		httpx.Error(w, http.StatusNotFound, "Collector profile not found")
		return
	}
	var created []store.Catalog
	err = db.WithTx(ctx, h.DB, func(tx pgx.Tx) error {
		var err error
		created, err = store.ReplaceCatalogs(ctx, tx, profile.ID, items)
		return err
	})
	if db.IsForeignKeyViolation(err) {
		httpx.Error(w, http.StatusBadRequest, "Kategori tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, created)
}

// GET /collector/profile — profil lapak sendiri beserta katalog.
func (h *Handler) GetCollectorProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := store.GetProfileByUser(ctx, h.DB, middleware.User(r).ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if p == nil {
		httpx.Error(w, http.StatusNotFound, "Profile not found")
		return
	}
	catalogs, err := store.CatalogsWithCategory(ctx, h.DB, p.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, struct {
		*store.CollectorProfile
		Catalogs []store.Catalog `json:"catalogs"`
	}{p, catalogs})
}
