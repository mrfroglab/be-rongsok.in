package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

const orderCols = `o.id, o."customerId", o."collectorId", o."categoryId", o.method::text, o."photoUrl", o."addressText",
	o."transactionProofUrl", o."estimatedWeight", o."actualWeight", o."agreedPrice", o."totalPrice", o.status::text,
	o."createdAt", o."updatedAt"`

func (o *Order) targets() []any {
	return []any{&o.ID, &o.CustomerID, &o.CollectorID, &o.CategoryID, &o.Method, &o.PhotoURL, &o.AddressText,
		&o.TransactionProofURL, &o.EstimatedWeight, &o.ActualWeight, &o.AgreedPrice, &o.TotalPrice, &o.Status,
		&o.CreatedAt, &o.UpdatedAt}
}

// GetOrder: nil bila tidak ada.
func GetOrder(ctx context.Context, q db.DBTX, id string) (*Order, error) {
	var o Order
	err := q.QueryRow(ctx, `SELECT `+orderCols+` FROM "Order" o WHERE o.id = $1`, id).Scan(o.targets()...)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

type NewOrderItem struct {
	CategoryID      string
	EstimatedWeight float64
	Notes           *string
}

// CreateOrder menyimpan order + item-itemnya (panggil di dalam transaksi).
// o.ID, CreatedAt & UpdatedAt diisi di sini.
func CreateOrder(ctx context.Context, q db.DBTX, o *Order, items []NewOrderItem) ([]OrderItem, error) {
	o.ID = NewID()
	now := Now()
	o.CreatedAt, o.UpdatedAt = Time{now}, Time{now}
	if _, err := q.Exec(ctx, `INSERT INTO "Order" (id, "customerId", "collectorId", "categoryId", method, "photoUrl",
			"addressText", "estimatedWeight", status, "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, $5::"OrderMethod", $6, $7, $8, $9::"OrderStatus", $10, $10)`,
		o.ID, o.CustomerID, o.CollectorID, o.CategoryID, o.Method, o.PhotoURL, o.AddressText, o.EstimatedWeight,
		o.Status, now); err != nil {
		return nil, err
	}
	out := make([]OrderItem, 0, len(items))
	for _, it := range items {
		item := OrderItem{ID: NewID(), OrderID: o.ID, CategoryID: it.CategoryID, EstimatedWeight: it.EstimatedWeight, Notes: it.Notes}
		if _, err := q.Exec(ctx, `INSERT INTO "OrderItem" (id, "orderId", "categoryId", "estimatedWeight", notes)
			VALUES ($1, $2, $3, $4, $5)`, item.ID, item.OrderID, item.CategoryID, item.EstimatedWeight, item.Notes); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func CountCustomerOrders(ctx context.Context, q db.DBTX, customerID, status string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM "Order" WHERE "customerId" = $1 AND status::text = $2`,
		customerID, status).Scan(&n)
	return n, err
}

// AddOfferRecipients mencatat pengepul yang dinotifikasi untuk sebuah order.
func AddOfferRecipients(ctx context.Context, q db.DBTX, orderID string, collectorIDs []string) error {
	if len(collectorIDs) == 0 {
		return nil
	}
	ids := make([]string, len(collectorIDs))
	for i := range ids {
		ids[i] = NewID()
	}
	_, err := q.Exec(ctx, `INSERT INTO "OrderCollector" (id, "orderId", "collectorId", status)
		SELECT unnest($1::text[]), $2, unnest($3::text[]), 'notified'`, ids, orderID, collectorIDs)
	return err
}

// IsOfferRecipient: pengepul dinotifikasi untuk order war ini dan belum menolak?
func IsOfferRecipient(ctx context.Context, q db.DBTX, orderID, collectorID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "OrderCollector"
		WHERE "orderId" = $1 AND "collectorId" = $2 AND status = 'notified')`, orderID, collectorID).Scan(&ok)
	return ok, err
}

// OfferRecipients: semua pengepul yang pernah dinotifikasi (apa pun statusnya).
func OfferRecipients(ctx context.Context, q db.DBTX, orderID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT "collectorId" FROM "OrderCollector" WHERE "orderId" = $1`, orderID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func RejectOffer(ctx context.Context, q db.DBTX, orderID, collectorID string) error {
	_, err := q.Exec(ctx, `UPDATE "OrderCollector" SET status = 'rejected' WHERE "orderId" = $1 AND "collectorId" = $2`,
		orderID, collectorID)
	return err
}

// SetStatus menambahkan status = <s> (cast ke enum) ke Sets.
func (s *Sets) SetStatus(status string) { s.AddRaw("status", s.Next(status)+`::"OrderStatus"`) }

// Transition mengubah order secara ATOMIK hanya bila status di DB masih salah
// satu dari `from` (dan, bila matchCollector, collectorId masih = collectorID;
// nil = masih kosong). Mencegah race dua pengepul accept bersamaan & double submit.
func Transition(ctx context.Context, q db.DBTX, id string, from []string, matchCollector bool, collectorID *string, s *Sets) (bool, error) {
	s.Add("updatedAt", Now())
	args := append([]any{}, s.Args...)
	args = append(args, id, from)
	where := `id = $` + itoa(len(args)-1) + ` AND status::text = ANY($` + itoa(len(args)) + `::text[])`
	if matchCollector {
		args = append(args, collectorID)
		where += ` AND "collectorId" IS NOT DISTINCT FROM $` + itoa(len(args)) + `::text`
	}
	tag, err := q.Exec(ctx, `UPDATE "Order" SET `+s.String()+` WHERE `+where, args...)
	return tag.RowsAffected() == 1, err
}

// ActiveCollectorStatuses = status order yang dihitung ke batas pesanan aktif pengepul.
var ActiveCollectorStatuses = []string{StatusConfirmed, StatusOnTheWay, StatusInProgress, StatusAwaitingConfirmation}

func CountActiveCollectorOrders(ctx context.Context, q db.DBTX, collectorID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM "Order" WHERE "collectorId" = $1 AND status::text = ANY($2::text[])`,
		collectorID, ActiveCollectorStatuses).Scan(&n)
	return n, err
}

// OrderItemIDs: id item sebuah order.
func OrderItemIDs(ctx context.Context, q db.DBTX, orderID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM "OrderItem" WHERE "orderId" = $1`, orderID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func SetItemWeighing(ctx context.Context, q db.DBTX, itemID string, weight, price float64) error {
	_, err := q.Exec(ctx, `UPDATE "OrderItem" SET "actualWeight" = $1, "agreedPrice" = $2 WHERE id = $3`, weight, price, itemID)
	return err
}

// ItemsWithCategory: item order (beserta kategori) dikelompokkan per orderId.
func ItemsWithCategory(ctx context.Context, q db.DBTX, orderIDs []string) (map[string][]OrderItem, error) {
	out := make(map[string][]OrderItem, len(orderIDs))
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT oi.id, oi."orderId", oi."categoryId", oi."estimatedWeight", oi."actualWeight", oi."agreedPrice", oi.notes,
		       `+categoryColsAs("c")+`
		FROM "OrderItem" oi JOIN "WasteCategory" c ON c.id = oi."categoryId"
		WHERE oi."orderId" = ANY($1::text[])`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it OrderItem
		var c Category
		if err := rows.Scan(&it.ID, &it.OrderID, &it.CategoryID, &it.EstimatedWeight, &it.ActualWeight, &it.AgreedPrice,
			&it.Notes, &c.ID, &c.Name, &c.Description, &c.IconURL, &c.Unit, &c.SortOrder, &c.ParentID); err != nil {
			return nil, err
		}
		it.Category = &c
		out[it.OrderID] = append(out[it.OrderID], it)
	}
	return out, rows.Err()
}

func CreateReceipt(ctx context.Context, q db.DBTX, orderID string, details any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO "Receipt" (id, "orderId", "issuedAt", "detailsJson") VALUES ($1, $2, $3, $4::jsonb)`,
		NewID(), orderID, Now(), string(b))
	return err
}

// ── Daftar order dengan relasi ─────────────────────────────────────────────

// OrderRow = order + customer + collector (+ shopName) + kategori + koordinat.
type OrderRow struct {
	Order
	Customer          PublicUser
	CustomerLoc       *LatLng
	Collector         *PublicUser
	CollectorLoc      *LatLng
	CollectorShopName *string
	HasShop           bool
	Category          *Category
}

const orderRowSelect = `SELECT ` + orderCols + `,
	cu.id, cu.name, cu.phone, cu."avatarUrl", cu."avgRating", cu."isVerified", cu."addressText", cu.role::text,
	ST_Y(cu.location::geometry), ST_X(cu.location::geometry),
	co.id, co.name, co.phone, co."avatarUrl", co."avgRating", co."isVerified", co."addressText", co.role::text,
	ST_Y(co.location::geometry), ST_X(co.location::geometry),
	cp.id, cp."shopName",
	` + `c.id, c.name, c.description, c."iconUrl", c.unit, c."sortOrder", c."parentId"
FROM "Order" o
JOIN "User" cu ON cu.id = o."customerId"
LEFT JOIN "User" co ON co.id = o."collectorId"
LEFT JOIN "CollectorProfile" cp ON cp."userId" = co.id
LEFT JOIN "WasteCategory" c ON c.id = o."categoryId"`

func queryOrderRows(ctx context.Context, q db.DBTX, where, tail string, args ...any) ([]OrderRow, error) {
	sql := orderRowSelect
	if where != "" {
		sql += " WHERE " + where
	}
	rows, err := q.Query(ctx, sql+" "+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrderRow{}
	for rows.Next() {
		var r OrderRow
		var custLat, custLng, collLat, collLng *float64
		var co struct {
			ID, Name, Role, AddressText, Phone, AvatarURL *string
			AvgRating                                     *float64
			IsVerified                                    *bool
		}
		var shopID *string
		var cat nullCategory
		targets := append(r.Order.targets(),
			&r.Customer.ID, &r.Customer.Name, &r.Customer.Phone, &r.Customer.AvatarURL, &r.Customer.AvgRating,
			&r.Customer.IsVerified, &r.Customer.AddressText, &r.Customer.Role, &custLat, &custLng,
			&co.ID, &co.Name, &co.Phone, &co.AvatarURL, &co.AvgRating, &co.IsVerified, &co.AddressText, &co.Role,
			&collLat, &collLng, &shopID, &r.CollectorShopName)
		targets = append(targets, cat.targets()...)
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		r.CustomerLoc = latLng(custLat, custLng)
		if co.ID != nil {
			r.Collector = &PublicUser{ID: *co.ID, Name: *co.Name, Phone: co.Phone, AvatarURL: co.AvatarURL,
				AvgRating: *co.AvgRating, IsVerified: *co.IsVerified, AddressText: co.AddressText, Role: *co.Role}
			r.CollectorLoc = latLng(collLat, collLng)
		}
		r.HasShop = shopID != nil
		r.Category = cat.value()
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetOrderRow: satu order dengan relasi; nil bila tidak ada.
func GetOrderRow(ctx context.Context, q db.DBTX, id string) (*OrderRow, error) {
	rows, err := queryOrderRows(ctx, q, "o.id = $1", "", id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// ListCollectorQueue: antrean PENDING pengepul = (1) order FORWARD yang
// ditujukan ke dia & belum ditolak, plus (2) order WAR (collectorId kosong) yang
// ditawarkan ke dia & belum ditolak.
func ListCollectorQueue(ctx context.Context, q db.DBTX, collectorID string, limit int) ([]OrderRow, error) {
	return queryOrderRows(ctx, q, `o.status = 'PENDING' AND (
		(o."collectorId" = $1 AND NOT EXISTS (SELECT 1 FROM "OrderCollector" oc
			WHERE oc."orderId" = o.id AND oc."collectorId" = $1 AND oc.status = 'rejected'))
		OR (o."collectorId" IS NULL AND EXISTS (SELECT 1 FROM "OrderCollector" oc
			WHERE oc."orderId" = o.id AND oc."collectorId" = $1 AND oc.status = 'notified')))`,
		`ORDER BY o."createdAt" DESC LIMIT $2`, collectorID, limit)
}

// ListOrdersBy: order milik user sebagai "customerId" atau "collectorId",
// opsional difilter status.
func ListOrdersBy(ctx context.Context, q db.DBTX, asCollector bool, userID, status string, limit int) ([]OrderRow, error) {
	col := `o."customerId"`
	if asCollector {
		col = `o."collectorId"`
	}
	return queryOrderRows(ctx, q, col+` = $1 AND ($2 = '' OR o.status::text = $2)`,
		`ORDER BY o."createdAt" DESC LIMIT $3`, userID, status, limit)
}

// ListAllOrders (admin): paginasi + total.
func ListAllOrders(ctx context.Context, q db.DBTX, status string, limit, offset int) ([]OrderRow, int, error) {
	rows, err := queryOrderRows(ctx, q, `($1 = '' OR o.status::text = $1)`,
		`ORDER BY o."createdAt" DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var total int
	err = q.QueryRow(ctx, `SELECT count(*) FROM "Order" WHERE ($1 = '' OR status::text = $1)`, status).Scan(&total)
	return rows, total, err
}

// RatingsForOrder: semua rating sebuah order.
func RatingsForOrder(ctx context.Context, q db.DBTX, orderID string) ([]Rating, error) {
	rows, err := q.Query(ctx, `SELECT `+ratingCols+` FROM "Rating" WHERE "orderId" = $1 ORDER BY "createdAt" ASC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rating{}
	for rows.Next() {
		var r Rating
		if err := rows.Scan(r.targets()...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Penyapu otomatis ──────────────────────────────────────────────────────

// CancelStaleOnTheWay membatalkan order ON_THE_WAY yang tak berubah sejak before.
func CancelStaleOnTheWay(ctx context.Context, q db.DBTX, before time.Time) ([]string, error) {
	rows, err := q.Query(ctx, `UPDATE "Order" SET status = 'CANCELLED', "updatedAt" = $1
		WHERE status = 'ON_THE_WAY' AND "updatedAt" < $2 RETURNING id`, Now(), before.UTC())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// CancelExpiredWar membatalkan order war (PENDING tanpa collector) yang dibuat sebelum before.
func CancelExpiredWar(ctx context.Context, q db.DBTX, before time.Time) ([]string, error) {
	rows, err := q.Query(ctx, `UPDATE "Order" SET status = 'CANCELLED', "updatedAt" = $1
		WHERE status = 'PENDING' AND "collectorId" IS NULL AND "createdAt" < $2 RETURNING id`, Now(), before.UTC())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
