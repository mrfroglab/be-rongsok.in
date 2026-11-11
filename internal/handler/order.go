package handler

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

const maxWeight = 100000

func forbidden(w http.ResponseWriter, msg string) { httpx.Error(w, http.StatusForbidden, msg) }

// POST /orders — customer membuat order (forward ke satu lapak, atau war/broadcast).
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	type itemIn struct {
		CategoryID      *string
		EstimatedWeight *float64
		Notes           *string
	}
	var in struct {
		CategoryID      *string
		EstimatedWeight *float64
		Notes           *string
		Method          *string
		PhotoURL        *string
		AddressText     *string
		Lat, Lng        *float64
		CollectorID     *string
		Items           []itemIn
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	v.Positive("estimatedWeight", in.EstimatedWeight, maxWeight)
	v.StrLen("notes", in.Notes, -1, 1000)
	if httpx.Required(v, "method", in.Method) {
		v.OneOf("method", in.Method, "PICKUP", "DROPOFF")
	}
	v.StrLen("addressText", in.AddressText, -1, 500)
	if httpx.Required(v, "lat", in.Lat) {
		v.Range("lat", in.Lat, -90, 90)
	}
	if httpx.Required(v, "lng", in.Lng) {
		v.Range("lng", in.Lng, -180, 180)
	}
	if len(in.Items) > 50 {
		v.Add("items", "Array must contain at most 50 element(s)")
	}
	for i, it := range in.Items {
		p := "items." + strconv.Itoa(i)
		httpx.Required(v, p+".categoryId", it.CategoryID)
		if httpx.Required(v, p+".estimatedWeight", it.EstimatedWeight) {
			v.Positive(p+".estimatedWeight", it.EstimatedWeight, maxWeight)
		}
		v.StrLen(p+".notes", it.Notes, -1, 1000)
	}
	if v.Fail(w) {
		return
	}

	ctx := r.Context()
	user := middleware.User(r)
	forwardTo := str(in.CollectorID)

	// Order forward wajib ditujukan ke pengepul sungguhan (punya profil lapak) dan
	// bukan ke diri sendiri (cegah wash-trading poin eco/leaderboard/rating).
	if forwardTo != "" {
		if forwardTo == user.ID {
			httpx.Error(w, http.StatusBadRequest, "Tidak bisa memesan ke lapak sendiri")
			return
		}
		target, err := store.GetAuthUser(ctx, h.DB, forwardTo)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		var profile *store.CollectorProfile
		if target != nil && target.Role == store.RoleCollector {
			if profile, err = store.GetProfileByUser(ctx, h.DB, forwardTo); err != nil {
				httpx.Internal(w, r, err)
				return
			}
		}
		if profile == nil {
			httpx.Error(w, http.StatusBadRequest, "Pengepul tujuan tidak ditemukan")
			return
		}
	}

	// Anti-scam: transaksi PERTAMA wajib DROP-OFF. PICKUP baru terbuka setelah
	// customer punya ≥1 order COMPLETED — mencegah pesanan jemput fiktif.
	if *in.Method == "PICKUP" {
		n, err := store.CountCustomerOrders(ctx, h.DB, user.ID, store.StatusCompleted)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if n == 0 {
			forbidden(w, "Transaksi pertama wajib antar sendiri (drop-off). Opsi dijemput terbuka setelah 1 transaksi selesai.")
			return
		}
	}

	var items []store.NewOrderItem
	switch {
	case len(in.Items) > 0:
		for _, it := range in.Items {
			items = append(items, store.NewOrderItem{CategoryID: *it.CategoryID, EstimatedWeight: *it.EstimatedWeight, Notes: nonEmpty(it.Notes)})
		}
	case str(in.CategoryID) != "" && in.EstimatedWeight != nil && *in.EstimatedWeight != 0:
		items = []store.NewOrderItem{{CategoryID: *in.CategoryID, EstimatedWeight: *in.EstimatedWeight, Notes: nonEmpty(in.Notes)}}
	default:
		httpx.Error(w, http.StatusUnprocessableEntity, "Either items array or both categoryId and estimatedWeight must be provided")
		return
	}

	total := 0.0
	categoryIDs := make([]string, len(items))
	for i, it := range items {
		total += it.EstimatedWeight
		categoryIDs[i] = it.CategoryID
	}
	order := store.Order{
		CustomerID: user.ID, Method: *in.Method, PhotoURL: in.PhotoURL, AddressText: nonEmpty(in.AddressText),
		EstimatedWeight: &total, Status: store.StatusPending,
	}
	if len(items) == 1 {
		order.CategoryID = &items[0].CategoryID
	}
	if forwardTo != "" {
		order.CollectorID = &forwardTo
	}

	var created []store.OrderItem
	var recipients []string
	err := db.WithTx(ctx, h.DB, func(tx pgx.Tx) error {
		var err error
		if created, err = store.CreateOrder(ctx, tx, &order, items); err != nil {
			return err
		}
		if forwardTo != "" {
			recipients = []string{forwardTo}
		} else {
			// Broadcast (REBUTAN) ke SEMUA pengepul yang BUKA — tanpa batas radius,
			// terdekat dulu. Pengepul bebas menolak kalau kejauhan.
			if recipients, err = store.BroadcastCandidates(ctx, tx, user.ID, *in.Lat, *in.Lng); err != nil {
				return err
			}
		}
		return store.AddOfferRecipients(ctx, tx, order.ID, recipients)
	})
	if db.IsForeignKeyViolation(err) {
		httpx.Error(w, http.StatusUnprocessableEntity, "Kategori tidak ditemukan")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}

	payloadItems := make([]map[string]any, len(items))
	for i, it := range items {
		payloadItems[i] = map[string]any{"categoryId": it.CategoryID, "estimatedWeight": it.EstimatedWeight}
	}
	event := map[string]any{
		"orderId":   order.ID,
		"createdAt": order.CreatedAt,
		// War (broadcast) = null; Forward (private) = userId pengepul tujuan.
		"collectorId": order.CollectorID,
		"category":    categoryIDs[0],
		"categories":  categoryIDs,
		"estWeight":   total,
		"method":      order.Method,
		"items":       payloadItems,
	}
	if in.PhotoURL != nil {
		// Foto live tumpukan rongsok (anti pesanan fiktif) — pengepul wajib lihat.
		event["photoUrl"] = *in.PhotoURL
	}
	for _, id := range recipients {
		h.Hub.Emit("collector:"+id, "new_order", event)
	}

	httpx.OK(w, http.StatusCreated, struct {
		store.Order
		Items []store.OrderItem `json:"items"`
	}{order, created})
}

// toAmount: angka berat/harga dari client — number atau string angka, finite, 0..1e9.
func toAmount(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case string:
		var err error
		if f, err = strconv.ParseFloat(x, 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= 1e9
}

var errConflict = errors.New("status changed")

// PATCH /orders/{id} — aksi status: accept | arrive | validate | confirm | reject | cancel.
func (h *Handler) UpdateOrderStatus(w http.ResponseWriter, r *http.Request) {
	body, ok := httpx.ReadBody(w, r)
	if !ok {
		return
	}
	var in map[string]any
	if !httpx.Unmarshal(w, body, &in) {
		return
	}
	action, _ := in["action"].(string)

	ctx := r.Context()
	user := middleware.User(r)
	id := chi.URLParam(r, "id")
	order, err := store.GetOrder(ctx, h.DB, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if order == nil {
		httpx.Error(w, http.StatusNotFound, "Order not found")
		return
	}
	isCustomer := order.CustomerID == user.ID
	isCollector := order.CollectorID != nil && *order.CollectorID == user.ID
	conflict := func() {
		httpx.Error(w, http.StatusConflict, "Status pesanan sudah berubah, silakan muat ulang.")
	}
	invalidState := func() { httpx.Error(w, http.StatusBadRequest, "Invalid state") }

	var newStatus string
	switch action {
	case "accept":
		if user.Role != store.RoleCollector {
			forbidden(w, "Hanya pengepul yang bisa menerima pesanan")
			return
		}
		if order.Status != store.StatusPending {
			httpx.Error(w, http.StatusBadRequest, "Order already taken")
			return
		}
		if order.CollectorID != nil {
			// Forward/private: hanya pengepul tujuan.
			if *order.CollectorID != user.ID {
				forbidden(w, "Pesanan ini ditujukan ke pengepul lain")
				return
			}
		} else {
			// War/broadcast: hanya pengepul yang dinotifikasi & belum menolak.
			ok, err := store.IsOfferRecipient(ctx, h.DB, id, user.ID)
			if err != nil {
				httpx.Internal(w, r, err)
				return
			}
			if !ok {
				forbidden(w, "Pesanan ini tidak ditawarkan ke Anda")
				return
			}
		}
		// Pengepul wajib terverifikasi KTP sebelum menerima pesanan.
		me, err := store.GetUserByID(ctx, h.DB, user.ID)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if me == nil || !me.IsVerified {
			forbidden(w, "Verifikasi identitas (KTP) dulu sebelum menerima pesanan")
			return
		}
		profile, err := store.GetProfileByUser(ctx, h.DB, user.ID)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if profile == nil {
			httpx.Error(w, http.StatusBadRequest, "Lengkapi profil lapak terlebih dahulu")
			return
		}
		// Hitung + ambil dalam satu transaksi dengan lock per pengepul,
		// agar accept paralel tidak melewati batas pesanan aktif.
		newStatus = store.StatusOnTheWay
		var won, overLimit bool
		err = db.WithTx(ctx, h.DB, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('accept:' || $1))`, user.ID); err != nil {
				return err
			}
			n, err := store.CountActiveCollectorOrders(ctx, tx, user.ID)
			if err != nil {
				return err
			}
			if n >= profile.MaxConcurrentOrders {
				overLimit = true
				return nil
			}
			// Diterima langsung jadi "Dalam Perjalanan". collectorId ikut dicocokkan:
			// war (null) hanya bisa dimenangkan satu pengepul.
			sets := &store.Sets{}
			sets.SetStatus(newStatus)
			sets.Add("collectorId", user.ID)
			won, err = store.Transition(ctx, tx, id, []string{store.StatusPending}, true, order.CollectorID, sets)
			return err
		})
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if overLimit {
			httpx.Error(w, http.StatusBadRequest, fmt.Sprintf(
				"Anda telah mencapai batas maksimal pesanan aktif (%d pesanan). Selesaikan pesanan yang ada terlebih dahulu.",
				profile.MaxConcurrentOrders))
			return
		}
		if !won {
			httpx.Error(w, http.StatusBadRequest, "Order already taken")
			return
		}

	case "arrive": // Pihak yang menuju lokasi menandai SUDAH SAMPAI (siap timbang).
		if !isCustomer && !isCollector {
			forbidden(w, "Anda bukan bagian dari pesanan ini")
			return
		}
		// Terima dari ON_THE_WAY (alur baru) maupun CONFIRMED (backward-compat).
		if order.Status != store.StatusOnTheWay && order.Status != store.StatusConfirmed {
			httpx.Error(w, http.StatusBadRequest, "Pesanan belum siap untuk ditandai sampai")
			return
		}
		newStatus = store.StatusInProgress
		sets := &store.Sets{}
		sets.SetStatus(newStatus)
		ok, err := store.Transition(ctx, h.DB, id, []string{store.StatusOnTheWay, store.StatusConfirmed}, false, nil, sets)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if !ok {
			conflict()
			return
		}

	case "validate": // Pengepul (yang ditetapkan) input berat & harga aktual.
		if !isCollector {
			forbidden(w, "Hanya pengepul pesanan ini yang bisa menimbang")
			return
		}
		if order.Status != store.StatusInProgress {
			invalidState()
			return
		}
		newStatus = store.StatusAwaitingConfirmation
		if !h.validateWeighing(w, r, order, in) {
			return
		}

	case "confirm": // Customer menyetujui harga → selesai + struk digital.
		if !isCustomer {
			forbidden(w, "Hanya customer pesanan ini yang bisa menyetujui")
			return
		}
		if order.Status != store.StatusAwaitingConfirmation {
			invalidState()
			return
		}
		newStatus = store.StatusCompleted
		err := db.WithTx(ctx, h.DB, func(tx pgx.Tx) error {
			sets := &store.Sets{}
			sets.SetStatus(newStatus)
			ok, err := store.Transition(ctx, tx, id, []string{store.StatusAwaitingConfirmation}, false, nil, sets)
			if err != nil {
				return err
			}
			if !ok {
				return errConflict
			}
			return createReceipt(ctx, tx, id)
		})
		if errors.Is(err, errConflict) {
			conflict()
			return
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}

	case "reject": // Pengepul menolak — order tetap PENDING, hilang dari antreannya.
		if err := store.RejectOffer(ctx, h.DB, id, user.ID); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		httpx.OK(w, http.StatusOK, map[string]any{"orderId": id, "status": order.Status, "rejected": true})
		return

	case "cancel": // Customer (atau pengepul terkait) membatalkan.
		cancellable := []string{store.StatusPending, store.StatusConfirmed, store.StatusOnTheWay, store.StatusInProgress}
		if !slices.Contains(cancellable, order.Status) {
			httpx.Error(w, http.StatusBadRequest, "Pesanan tidak bisa dibatalkan pada status ini")
			return
		}
		if !isCustomer && !isCollector {
			forbidden(w, "Anda tidak berhak membatalkan pesanan ini")
			return
		}
		newStatus = store.StatusCancelled
		sets := &store.Sets{}
		sets.SetStatus(newStatus)
		ok, err := store.Transition(ctx, h.DB, id, cancellable, false, nil, sets)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if !ok {
			conflict()
			return
		}

	default:
		httpx.Error(w, http.StatusBadRequest, "Invalid action")
		return
	}

	h.Hub.Emit("order:"+id, "order_status_update", map[string]any{"orderId": id, "status": newStatus})
	// Order keluar dari pool PENDING (diambil / dibatalkan) → bersihkan antrean
	// SEMUA pengepul yang sempat dinotifikasi (first-come-first-served instan).
	if action == "accept" || action == "cancel" {
		h.emitOrderTaken(ctx, id)
	}
	httpx.OK(w, http.StatusOK, map[string]any{"orderId": id, "status": newStatus})
}

func (h *Handler) emitOrderTaken(ctx context.Context, orderID string) {
	ids, err := store.OfferRecipients(ctx, h.DB, orderID)
	if err != nil {
		return
	}
	for _, c := range ids {
		h.Hub.Emit("collector:"+c, "order_taken", map[string]any{"orderId": orderID})
	}
}

// validateWeighing: aksi "validate" — simpan berat & harga aktual (multi-item
// atau single-item fallback) lalu status → AWAITING_CONFIRMATION.
func (h *Handler) validateWeighing(w http.ResponseWriter, r *http.Request, order *store.Order, in map[string]any) bool {
	ctx := r.Context()
	if proof, present := in["transactionProofUrl"]; present && proof != nil {
		if s, ok := proof.(string); !ok || len(s) > 2048 {
			httpx.Error(w, http.StatusUnprocessableEntity, "transactionProofUrl tidak valid")
			return false
		}
	}
	proofURL, _ := in["transactionProofUrl"].(string)

	ownIDs, err := store.OrderItemIDs(ctx, h.DB, order.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return false
	}
	invalid := func() bool {
		httpx.Error(w, http.StatusUnprocessableEntity, "Data timbangan tidak valid")
		return false
	}

	type update struct {
		id            string
		weight, price float64
	}
	var updates []update
	var single *update
	seen := map[string]bool{}
	if list, ok := in["items"].([]any); ok && len(list) > 0 {
		for _, raw := range list {
			item, _ := raw.(map[string]any)
			if item == nil {
				return invalid()
			}
			itemID, _ := item["id"].(string)
			weight, okW := toAmount(item["actualWeight"])
			price, okP := toAmount(item["agreedPrice"])
			if !slices.Contains(ownIDs, itemID) || seen[itemID] || !okW || !okP {
				return invalid()
			}
			seen[itemID] = true
			updates = append(updates, update{itemID, weight, price})
		}
	} else {
		weight, okW := toAmount(in["actualWeight"])
		price, okP := toAmount(in["agreedPrice"])
		if !okW || !okP {
			return invalid()
		}
		single = &update{weight: weight, price: price}
		if len(ownIDs) > 0 {
			updates = []update{{ownIDs[0], weight, price}}
		}
	}

	var totalWeight, totalPrice float64
	if single != nil {
		totalWeight, totalPrice = single.weight, single.weight*single.price
	} else {
		for _, u := range updates {
			totalWeight += u.weight
			totalPrice += u.weight * u.price
		}
	}

	err = db.WithTx(ctx, h.DB, func(tx pgx.Tx) error {
		sets := &store.Sets{}
		sets.SetStatus(store.StatusAwaitingConfirmation)
		sets.Add("actualWeight", totalWeight)
		sets.Add("totalPrice", totalPrice)
		if single != nil {
			sets.Add("agreedPrice", single.price)
		}
		var proof *string
		if proofURL != "" {
			proof = &proofURL
		}
		sets.Add("transactionProofUrl", proof)
		ok, err := store.Transition(ctx, tx, order.ID, []string{store.StatusInProgress}, false, nil, sets)
		if err != nil {
			return err
		}
		if !ok {
			return errConflict
		}
		for _, u := range updates {
			if err := store.SetItemWeighing(ctx, tx, u.id, u.weight, u.price); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errConflict) {
		httpx.Error(w, http.StatusConflict, "Status pesanan sudah berubah, silakan muat ulang.")
		return false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return false
	}
	return true
}

// createReceipt membuat struk digital untuk order yang baru COMPLETED.
func createReceipt(ctx context.Context, tx pgx.Tx, orderID string) error {
	o, err := store.GetOrder(ctx, tx, orderID)
	if err != nil {
		return err
	}
	byOrder, err := store.ItemsWithCategory(ctx, tx, []string{orderID})
	if err != nil {
		return err
	}
	items := []map[string]any{}
	for _, it := range byOrder[orderID] {
		items = append(items, map[string]any{
			"category":        it.Category.Name,
			"estimatedWeight": it.EstimatedWeight,
			"actualWeight":    it.ActualWeight,
			"agreedPrice":     it.AgreedPrice,
			"notes":           it.Notes,
			"total":           deref(it.ActualWeight) * deref(it.AgreedPrice),
		})
	}
	total := deref(o.ActualWeight) * deref(o.AgreedPrice)
	if o.TotalPrice != nil {
		total = *o.TotalPrice
	}
	details := map[string]any{
		"customer":            o.CustomerID,
		"collector":           o.CollectorID,
		"total":               total,
		"transactionProofUrl": o.TransactionProofURL,
	}
	if len(items) > 0 {
		details["items"] = items
	}
	return store.CreateReceipt(ctx, tx, orderID, details)
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ── Baca order ─────────────────────────────────────────────────────────────

type shopName struct {
	ShopName string `json:"shopName"`
}

type collectorWithShop struct {
	store.PublicUser
	CollectorProfile any `json:"collectorProfile"`
}

type orderListItem struct {
	store.Order
	Customer     store.PublicUser   `json:"customer"`
	Collector    *collectorWithShop `json:"collector"`
	Category     *store.Category    `json:"category"`
	Items        []store.OrderItem  `json:"items"`
	CustomerLat  *float64           `json:"customerLat"`
	CustomerLng  *float64           `json:"customerLng"`
	CollectorLat *float64           `json:"collectorLat"`
	CollectorLng *float64           `json:"collectorLng"`
	DistanceKm   *float64           `json:"distanceKm"`
}

func coords(l *store.LatLng) (lat, lng *float64) {
	if l == nil {
		return nil, nil
	}
	return &l.Lat, &l.Lng
}

// GET /orders?status=&role=customer|collector&limit= — order milik user.
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := middleware.User(r)
	q := r.URL.Query()
	status, role := q.Get("status"), q.Get("role")
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit == 0 {
		limit = 10
	}
	limit = min(max(limit, 1), 100)

	var rows []store.OrderRow
	switch {
	case role == "collector" && status == store.StatusPending:
		// Antrean: FORWARD (collectorId = dia) + WAR (collectorId null, ditawarkan
		// ke dia). FE membedakan keduanya dari collectorId.
		rows, err = store.ListCollectorQueue(ctx, h.DB, user.ID, limit)
	case role == "collector":
		rows, err = store.ListOrdersBy(ctx, h.DB, true, user.ID, status, limit)
	default:
		rows, err = store.ListOrdersBy(ctx, h.DB, false, user.ID, status, limit)
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	items, err := store.ItemsWithCategory(ctx, h.DB, orderIDs(rows))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Untuk antrean broadcast (belum ber-collector), titik pengepul = lokasi
	// pengepul yang sedang melihat.
	var viewer *store.LatLng
	if role == "collector" {
		if viewer, err = store.GetUserCoords(ctx, h.DB, user.ID); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}

	out := make([]orderListItem, 0, len(rows))
	for _, row := range rows {
		it := orderListItem{Order: row.Order, Customer: row.Customer, Category: row.Category, Items: orEmpty(items[row.ID])}
		if row.Collector != nil {
			it.Collector = &collectorWithShop{PublicUser: *row.Collector}
			if row.HasShop {
				it.Collector.CollectorProfile = shopName{ShopName: str(row.CollectorShopName)}
			}
		}
		collLoc := row.CollectorLoc
		if collLoc == nil {
			collLoc = viewer
		}
		it.CustomerLat, it.CustomerLng = coords(row.CustomerLoc)
		it.CollectorLat, it.CollectorLng = coords(collLoc)
		if row.CustomerLoc != nil && collLoc != nil {
			d := haversineKm(*row.CustomerLoc, *collLoc)
			it.DistanceKm = &d
		}
		out = append(out, it)
	}
	httpx.OK(w, http.StatusOK, out)
}

type orderDetail struct {
	store.Order
	Customer     store.PublicUser   `json:"customer"`
	Collector    *collectorWithShop `json:"collector"`
	Category     *store.Category    `json:"category"`
	Items        []store.OrderItem  `json:"items"`
	Ratings      []store.Rating     `json:"ratings"`
	CustomerLat  *float64           `json:"customerLat"`
	CustomerLng  *float64           `json:"customerLng"`
	CollectorLat *float64           `json:"collectorLat"`
	CollectorLng *float64           `json:"collectorLng"`
}

// GET /orders/{id} — detail order (pihak transaksi, kandidat war, admin).
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, err := store.GetOrderRow(ctx, h.DB, chi.URLParam(r, "id"))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if row == nil {
		httpx.OK(w, http.StatusOK, nil)
		return
	}
	ok, err := store.CanViewOrder(ctx, h.DB, middleware.User(r), &row.Order)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !ok {
		forbidden(w, "Anda tidak berhak melihat pesanan ini")
		return
	}
	items, err := store.ItemsWithCategory(ctx, h.DB, []string{row.ID})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	ratings, err := store.RatingsForOrder(ctx, h.DB, row.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	d := orderDetail{Order: row.Order, Customer: row.Customer, Category: row.Category,
		Items: orEmpty(items[row.ID]), Ratings: ratings}
	if row.Collector != nil {
		d.Collector = &collectorWithShop{PublicUser: *row.Collector}
		profile, err := store.GetProfileByUser(ctx, h.DB, row.Collector.ID)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if profile != nil {
			d.Collector.CollectorProfile = profile
		}
	}
	// Koordinat kedua pihak untuk peta rute antar/jemput di FE.
	d.CustomerLat, d.CustomerLng = coords(row.CustomerLoc)
	d.CollectorLat, d.CollectorLng = coords(row.CollectorLoc)
	httpx.OK(w, http.StatusOK, d)
}

func orderIDs(rows []store.OrderRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// haversineKm: jarak garis lurus (km) antara dua titik.
func haversineKm(a, b store.LatLng) float64 {
	const R = 6371.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(b.Lat-a.Lat), rad(b.Lng-a.Lng)
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Pow(math.Sin(dLng/2), 2)
	return 2 * R * math.Asin(math.Sqrt(h))
}
