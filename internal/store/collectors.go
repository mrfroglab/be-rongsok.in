package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

const profileCols = `id, "userId", "shopName", description, "shopImageUrl", "radiusKm", "isOpen",
	"operatingHours", "isPremium", "priorityScore", "maxConcurrentOrders"`

func scanProfile(row pgx.Row) (*CollectorProfile, error) {
	var p CollectorProfile
	err := row.Scan(&p.ID, &p.UserID, &p.ShopName, &p.Description, &p.ShopImageURL, &p.RadiusKm, &p.IsOpen,
		&p.OperatingHours, &p.IsPremium, &p.PriorityScore, &p.MaxConcurrentOrders)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func GetProfileByUser(ctx context.Context, q db.DBTX, userID string) (*CollectorProfile, error) {
	return scanProfile(q.QueryRow(ctx, `SELECT `+profileCols+` FROM "CollectorProfile" WHERE "userId" = $1`, userID))
}

func GetProfileByID(ctx context.Context, q db.DBTX, id string) (*CollectorProfile, error) {
	return scanProfile(q.QueryRow(ctx, `SELECT `+profileCols+` FROM "CollectorProfile" WHERE id = $1`, id))
}

// ProfileInput: field nil = tidak diubah (saat update) / default (saat create).
type ProfileInput struct {
	ShopName       string
	Description    *string
	RadiusKm       *float64
	IsOpen         *bool
	ShopImageURL   *string
	OperatingHours *string
}

// UpsertProfile membuat profil lapak atau memperbarui field yang dikirim saja.
func UpsertProfile(ctx context.Context, q db.DBTX, userID string, in ProfileInput) (*CollectorProfile, error) {
	// COALESCE(EXCLUDED.x ...) tak bisa dipakai karena NULL = "tidak dikirim";
	// pakai flag eksplisit per kolom.
	return scanProfile(q.QueryRow(ctx, `
		INSERT INTO "CollectorProfile" (id, "userId", "shopName", description, "radiusKm", "isOpen", "shopImageUrl", "operatingHours")
		VALUES ($1, $2, $3, $4, COALESCE($5::float8, 5), COALESCE($6::boolean, true), $7, $8)
		ON CONFLICT ("userId") DO UPDATE SET
			"shopName" = EXCLUDED."shopName",
			description      = CASE WHEN $9  THEN EXCLUDED.description      ELSE "CollectorProfile".description END,
			"radiusKm"       = CASE WHEN $10 THEN EXCLUDED."radiusKm"       ELSE "CollectorProfile"."radiusKm" END,
			"isOpen"         = CASE WHEN $11 THEN EXCLUDED."isOpen"         ELSE "CollectorProfile"."isOpen" END,
			"shopImageUrl"   = CASE WHEN $12 THEN EXCLUDED."shopImageUrl"   ELSE "CollectorProfile"."shopImageUrl" END,
			"operatingHours" = CASE WHEN $13 THEN EXCLUDED."operatingHours" ELSE "CollectorProfile"."operatingHours" END
		RETURNING `+profileCols,
		NewID(), userID, in.ShopName, in.Description, in.RadiusKm, in.IsOpen, in.ShopImageURL, in.OperatingHours,
		in.Description != nil, in.RadiusKm != nil, in.IsOpen != nil, in.ShopImageURL != nil, in.OperatingHours != nil))
}

type CatalogInput struct {
	CategoryID string
	MinPrice   float64
	MaxPrice   float64
	IsActive   bool
}

// ReplaceCatalogs menghapus katalog lama lalu membuat yang baru (panggil di dalam transaksi).
func ReplaceCatalogs(ctx context.Context, q db.DBTX, profileID string, items []CatalogInput) ([]Catalog, error) {
	if _, err := q.Exec(ctx, `DELETE FROM "CollectorCatalog" WHERE "collectorId" = $1`, profileID); err != nil {
		return nil, err
	}
	out := make([]Catalog, 0, len(items))
	for _, it := range items {
		c := Catalog{ID: NewID(), CollectorID: profileID, CategoryID: it.CategoryID,
			MinPrice: it.MinPrice, MaxPrice: it.MaxPrice, IsActive: it.IsActive}
		if _, err := q.Exec(ctx, `INSERT INTO "CollectorCatalog" (id, "collectorId", "categoryId", "minPrice", "maxPrice", "isActive")
			VALUES ($1, $2, $3, $4, $5, $6)`, c.ID, c.CollectorID, c.CategoryID, c.MinPrice, c.MaxPrice, c.IsActive); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// CatalogsWithCategory: katalog sebuah lapak beserta kategorinya.
func CatalogsWithCategory(ctx context.Context, q db.DBTX, profileID string) ([]Catalog, error) {
	rows, err := q.Query(ctx, `
		SELECT cc.id, cc."collectorId", cc."categoryId", cc."minPrice", cc."maxPrice", cc."isActive", `+categoryColsAs("c")+`
		FROM "CollectorCatalog" cc JOIN "WasteCategory" c ON c.id = cc."categoryId"
		WHERE cc."collectorId" = $1
		ORDER BY c."sortOrder" ASC, c.name ASC`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Catalog{}
	for rows.Next() {
		var cc Catalog
		var cat Category
		if err := rows.Scan(&cc.ID, &cc.CollectorID, &cc.CategoryID, &cc.MinPrice, &cc.MaxPrice, &cc.IsActive,
			&cat.ID, &cat.Name, &cat.Description, &cat.IconURL, &cat.Unit, &cat.SortOrder, &cat.ParentID); err != nil {
			return nil, err
		}
		cc.Category = &cat
		out = append(out, cc)
	}
	return out, rows.Err()
}

// CollectorOwner = data pemilik lapak di halaman detail publik.
type CollectorOwner struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Phone       *string  `json:"phone"`
	AvatarURL   *string  `json:"avatarUrl"`
	AvgRating   float64  `json:"avgRating"`
	AddressText *string  `json:"addressText"`
	Lat         *float64 `json:"lat,omitempty"`
	Lng         *float64 `json:"lng,omitempty"`
}

func GetCollectorOwner(ctx context.Context, q db.DBTX, userID string) (*CollectorOwner, error) {
	var o CollectorOwner
	err := q.QueryRow(ctx, `SELECT id, name, phone, "avatarUrl", "avgRating", "addressText",
		ST_Y(location::geometry), ST_X(location::geometry) FROM "User" WHERE id = $1`, userID).Scan(
		&o.ID, &o.Name, &o.Phone, &o.AvatarURL, &o.AvgRating, &o.AddressText, &o.Lat, &o.Lng)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if o.Lat == nil || o.Lng == nil {
		o.Lat, o.Lng = nil, nil
	}
	return &o, nil
}

// SearchResult = satu baris hasil pencarian pengepul terdekat.
type SearchResult struct {
	ID            string   `json:"id"`
	UserID        string   `json:"userId"`
	ShopName      string   `json:"shopName"`
	Description   *string  `json:"description"`
	ShopImageURL  *string  `json:"shopImageUrl"`
	PriorityScore int      `json:"priorityScore"`
	IsPremium     bool     `json:"isPremium"`
	OwnerName     string   `json:"ownerName"`
	AvatarURL     *string  `json:"avatarUrl"`
	AvgRating     float64  `json:"avgRating"`
	OwnerVerified bool     `json:"ownerVerified"`
	Distance      float64  `json:"distance"`
	MaxPrice      *float64 `json:"maxPrice"`
}

// SearchCollectors: lapak BUKA dalam radius (meter dari radiusKm), premium dulu
// lalu terdekat. categoryID opsional (induk → cocok bila punya salah satu anaknya).
// maxPrice = harga ambil tertinggi (untuk kategori itu bila difilter).
func SearchCollectors(ctx context.Context, q db.DBTX, lat, lng, radiusKm float64, categoryID string) ([]SearchResult, error) {
	categoryFilter := ""
	args := []any{lng, lat, radiusKm}
	if categoryID != "" {
		args = append(args, categoryID)
		categoryFilter = `AND cc."isActive" = true AND cc."categoryId" IN (
			SELECT id FROM "WasteCategory" WHERE id = $4 OR "parentId" = $4)`
	}
	rows, err := q.Query(ctx, `
		SELECT cp.id, cp."userId", cp."shopName", cp.description, cp."shopImageUrl", cp."priorityScore", cp."isPremium",
		       u.name, u."avatarUrl", u."avgRating", u."isVerified",
		       ST_Distance(u.location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography) AS distance,
		       MAX(CASE WHEN cc."isActive" = true THEN cc."maxPrice" END)
		FROM "CollectorProfile" cp
		JOIN "User" u ON cp."userId" = u.id
		LEFT JOIN "CollectorCatalog" cc ON cp.id = cc."collectorId"
		WHERE cp."isOpen" = true `+categoryFilter+`
		  AND u.location IS NOT NULL
		  AND ST_DWithin(u.location, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3::float8 * 1000)
		GROUP BY cp.id, cp."userId", u.id
		ORDER BY cp."isPremium" DESC, distance ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchResult{}
	for rows.Next() {
		var s SearchResult
		if err := rows.Scan(&s.ID, &s.UserID, &s.ShopName, &s.Description, &s.ShopImageURL, &s.PriorityScore, &s.IsPremium,
			&s.OwnerName, &s.AvatarURL, &s.AvgRating, &s.OwnerVerified, &s.Distance, &s.MaxPrice); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// BroadcastCandidates: userId semua pengepul BUKA yang punya lokasi (kecuali
// excludeUserID), terdekat dulu. Tanpa batas radius — pengepul bebas menolak.
func BroadcastCandidates(ctx context.Context, q db.DBTX, excludeUserID string, lat, lng float64) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT cp."userId"
		FROM "CollectorProfile" cp JOIN "User" u ON cp."userId" = u.id
		WHERE cp."isOpen" = true AND u.location IS NOT NULL AND u.id <> $1
		ORDER BY ST_Distance(u.location, ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography) ASC`,
		excludeUserID, lng, lat)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
