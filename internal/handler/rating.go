package handler

import (
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

// POST /ratings — rating untuk lawan transaksi pada order COMPLETED.
func (h *Handler) SubmitRating(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OrderID, RateeID     *string
		Score                *float64
		ReviewText, PhotoURL *string
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	httpx.Required(v, "orderId", in.OrderID)
	httpx.Required(v, "rateeId", in.RateeID)
	if httpx.Required(v, "score", in.Score) {
		if *in.Score != math.Trunc(*in.Score) || *in.Score < 1 || *in.Score > 5 {
			v.Add("score", "Score harus bilangan bulat 1–5")
		}
	}
	v.StrLen("reviewText", in.ReviewText, -1, 2000)
	v.StrLen("photoUrl", in.PhotoURL, -1, 2048)
	if v.Fail(w) {
		return
	}

	ctx := r.Context()
	user := middleware.User(r)
	order, err := store.GetOrder(ctx, h.DB, *in.OrderID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if order == nil || order.Status != store.StatusCompleted || order.CollectorID == nil {
		httpx.Error(w, http.StatusBadRequest, "Rating hanya bisa diberikan untuk transaksi yang sudah selesai.")
		return
	}
	var counterpart string
	switch user.ID {
	case order.CustomerID:
		counterpart = *order.CollectorID
	case *order.CollectorID:
		counterpart = order.CustomerID
	default:
		forbidden(w, "Anda bukan bagian dari transaksi ini.")
		return
	}
	if *in.RateeID != counterpart {
		httpx.Error(w, http.StatusBadRequest, "Penerima rating tidak sesuai dengan transaksi.")
		return
	}

	var rating *store.Rating
	err = db.WithTx(ctx, h.DB, func(tx pgx.Tx) error {
		var err error
		rating, err = store.CreateRating(ctx, tx, store.NewRating{
			OrderID: *in.OrderID, RaterID: user.ID, RateeID: counterpart, Score: int(*in.Score),
			ReviewText: in.ReviewText, PhotoURL: in.PhotoURL,
		})
		return err
	})
	if db.IsUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "Kamu sudah memberi rating untuk transaksi ini.")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusCreated, rating)
}

// GET /ratings/user/{userId} — rating yang diterima seorang user.
func (h *Handler) UserRatings(w http.ResponseWriter, r *http.Request) {
	ratings, err := store.RatingsForUser(r.Context(), h.DB, chi.URLParam(r, "userId"))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, ratings)
}
