package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

var dayNames = [7]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}

type weeklyPoint struct {
	Day    string  `json:"day"`
	Date   string  `json:"date"`
	Weight float64 `json:"weight"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

// GET /admin/stats — KPI + grafik 7 hari terakhir (tanggal UTC).
func (h *Handler) AdminStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := store.GetAdminTotals(ctx, h.DB)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -6)
	daily, err := store.DailyCompleted(ctx, h.DB, since)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	weekly := make([]weeklyPoint, 7)
	for i := range weekly {
		d := since.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		day := daily[key]
		weekly[i] = weeklyPoint{Day: dayNames[d.Weekday()], Date: key, Weight: day.Weight, Amount: day.Amount, Count: day.Count}
	}
	httpx.OK(w, http.StatusOK, map[string]any{
		"totalWeightKg":      t.TotalWeightKg,
		"totalPayout":        t.TotalPayout,
		"activeOrders":       t.ActiveOrders,
		"totalCustomers":     t.TotalCustomers,
		"totalCollectors":    t.TotalCollectors,
		"weeklyTransactions": weekly,
	})
}

type idName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type adminOrder struct {
	store.Order
	Customer  idName            `json:"customer"`
	Collector *idName           `json:"collector"`
	Category  *store.Category   `json:"category"`
	Items     []store.OrderItem `json:"items"`
}

// GET /admin/orders?status=&page=&limit= — monitoring semua transaksi.
func (h *Handler) AdminOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	limit = min(limit, 500)
	page, err := strconv.Atoi(q.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	rows, total, err := store.ListAllOrders(ctx, h.DB, q.Get("status"), limit, (page-1)*limit)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	items, err := store.ItemsWithCategory(ctx, h.DB, orderIDs(rows))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]adminOrder, 0, len(rows))
	for _, row := range rows {
		o := adminOrder{Order: row.Order, Customer: idName{row.Customer.ID, row.Customer.Name},
			Category: row.Category, Items: orEmpty(items[row.ID])}
		if row.Collector != nil {
			o.Collector = &idName{row.Collector.ID, row.Collector.Name}
		}
		out = append(out, o)
	}
	httpx.OK(w, http.StatusOK, map[string]any{"data": out, "total": total, "page": page, "limit": limit})
}

// ── CRUD kategori (data master) ─────────────────────────────────────────

type categoryInput struct {
	Name        *string
	Description *string
	IconURL     *string
	ParentID    httpx.Opt[string] // null = jadikan kategori induk
	Unit        *string
	SortOrder   *float64
}

func (in *categoryInput) validate(v *httpx.V, partial bool) {
	if !partial {
		httpx.Required(v, "name", in.Name)
	}
	v.StrLen("name", in.Name, 2, -1)
	v.OneOf("unit", in.Unit, "kg", "liter", "pcs")
	if in.SortOrder != nil && (*in.SortOrder != float64(int(*in.SortOrder))) {
		v.Add("sortOrder", "Expected integer")
	}
}

func (h *Handler) categoryWriteError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case db.IsUniqueViolation(err):
		httpx.Error(w, http.StatusBadRequest, "Nama kategori sudah ada")
	case db.IsForeignKeyViolation(err):
		httpx.Error(w, http.StatusBadRequest, "Kategori induk tidak ditemukan")
	default:
		httpx.Internal(w, r, err)
	}
}

// POST /admin/categories
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	in.validate(v, false)
	if v.Fail(w) {
		return
	}
	n := store.NewCategory{Name: *in.Name, Description: in.Description, IconURL: in.IconURL, Unit: in.Unit}
	if in.ParentID.Set && !in.ParentID.Null {
		n.ParentID = &in.ParentID.V
	}
	if in.SortOrder != nil {
		s := int(*in.SortOrder)
		n.SortOrder = &s
	}
	cat, err := store.CreateCategory(r.Context(), h.DB, n)
	if err != nil {
		h.categoryWriteError(w, r, err)
		return
	}
	httpx.OK(w, http.StatusCreated, cat)
}

// PATCH /admin/categories/{id}
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	in.validate(v, true)
	if v.Fail(w) {
		return
	}
	id := chi.URLParam(r, "id")
	sets := &store.Sets{}
	if in.Name != nil {
		sets.Add("name", *in.Name)
	}
	if in.Description != nil {
		sets.Add("description", *in.Description)
	}
	if in.IconURL != nil {
		sets.Add("iconUrl", *in.IconURL)
	}
	if in.ParentID.Set {
		var parent *string
		if !in.ParentID.Null {
			if in.ParentID.V == id {
				httpx.Error(w, http.StatusBadRequest, "Kategori tidak bisa menjadi induk dirinya sendiri")
				return
			}
			parent = &in.ParentID.V
		}
		sets.Add("parentId", parent)
	}
	if in.Unit != nil {
		sets.Add("unit", *in.Unit)
	}
	if in.SortOrder != nil {
		sets.Add("sortOrder", int(*in.SortOrder))
	}
	cat, err := store.UpdateCategory(r.Context(), h.DB, id, sets)
	if err != nil {
		h.categoryWriteError(w, r, err)
		return
	}
	if cat == nil {
		httpx.Error(w, http.StatusNotFound, "Kategori tidak ditemukan")
		return
	}
	httpx.OK(w, http.StatusOK, cat)
}

// DELETE /admin/categories/{id} — hanya bila tak punya sub-item & tak dipakai.
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	hasChildren, inUse, err := store.CategoryUsage(ctx, h.DB, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if hasChildren {
		httpx.Error(w, http.StatusBadRequest, "Kategori utama masih punya sub-item. Hapus sub-item dulu.")
		return
	}
	if inUse {
		httpx.Error(w, http.StatusBadRequest, "Kategori masih dipakai pengepul atau transaksi. Tidak bisa dihapus.")
		return
	}
	deleted, err := store.DeleteCategory(ctx, h.DB, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !deleted {
		httpx.Error(w, http.StatusNotFound, "Kategori tidak ditemukan")
		return
	}
	httpx.OK(w, http.StatusOK, map[string]string{"id": id})
}
