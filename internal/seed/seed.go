// Package seed: data demo (kategori, admin, pengepul, customer + riwayat order).
//
// PENGAMAN: hanya jalan bila DB masih kosong (tanpa user), atau dipaksa lewat
// `rongsokin-api seed --force` yang MENGHAPUS SEMUA DATA. Dulu seeder Node
// dipanggil di setiap start container → DB produksi terhapus tiap redeploy.
package seed

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math/big"
	mrand "math/rand/v2"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/security"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

type category struct{ name, unit, description string }

var categories = []category{
	{"Plastik", "kg", "Botol & gelas bening, emberan, botol sabun/oli, terpal, karung, plastik LIT/bening, mainan, & plastik keras"},
	{"Kertas & Kardus", "kg", "Kardus packing, kertas putih/HVS, buku, koran, majalah, & kertas duplex (kotak kemasan)"},
	{"Logam & Besi", "kg", "Besi tua/seng, aluminium (kaleng/wajan), kuningan (keran/gembok), kabel, & tembaga"},
	{"Kaca & Botol", "kg", "Botol kaca utuh (kecap/sirup/bir) & pecahan kaca/beling kiloan"},
	{"Elektronik", "pcs", "Aki bekas, TV/kulkas/mesin cuci rusak, kompresor, HP/laptop mati, & komponen sirkuit"},
	{"Lain-lain", "liter", "Minyak jelantah (minyak goreng bekas)"},
}

type customerSpec struct {
	name, email     string
	weightKg        float64
	hasCancelledOrd bool
}

var customers = []customerSpec{
	{"Rizky Customer", "rizky@example.com", 15.0, true},
	{"Tegar Pemula Pasif", "tegar.pasif@example.com", 0, false},
	{"Arif Pemula Hijau", "arif.pemula@example.com", 5.5, false},
	{"Dinda Pejuang", "dinda.pejuang@example.com", 28.3, false},
	{"Gita Pahlawan", "gita.pahlawan@example.com", 76.5, false},
	{"Bintang Legenda", "bintang.legenda@example.com", 135.2, true},
}

// IfEmpty mengisi data demo hanya bila belum ada user.
func IfEmpty(ctx context.Context, pool *pgxpool.Pool, adminPassword string) error {
	n, err := store.CountUsers(ctx, pool)
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Info("seeder dilewati — DB sudah berisi user", "users", n)
		return nil
	}
	return run(ctx, pool, adminPassword, false)
}

// Force menghapus SEMUA data lalu mengisi ulang data demo.
func Force(ctx context.Context, pool *pgxpool.Pool, adminPassword string) error {
	return run(ctx, pool, adminPassword, true)
}

func run(ctx context.Context, pool *pgxpool.Pool, adminPassword string, wipe bool) error {
	if adminPassword == "" {
		b := make([]byte, 18)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		adminPassword = base64.RawURLEncoding.EncodeToString(b)
		slog.Info("SEED_ADMIN_PASSWORD tidak diset — admin dibuat dengan password acak (reset lewat lupa password)")
	}
	return db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if wipe {
			slog.Warn("seed --force: menghapus semua data")
			if _, err := tx.Exec(ctx, `TRUNCATE "Rating", "Receipt", "OrderCollector", "OrderItem", "Order",
				"CollectorCatalog", "CollectorProfile", "User", "WasteCategory"`); err != nil {
				return err
			}
		}
		return seed(ctx, tx, adminPassword)
	})
}

func seed(ctx context.Context, tx pgx.Tx, adminPassword string) error {
	catIDs := map[string]string{}
	for i, c := range categories {
		id := store.NewID()
		catIDs[c.name] = id
		if _, err := tx.Exec(ctx, `INSERT INTO "WasteCategory" (id, name, description, unit, "sortOrder") VALUES ($1, $2, $3, $4, $5)`,
			id, c.name, c.description, c.unit, i); err != nil {
			return err
		}
	}

	adminHash, err := security.HashPassword(adminPassword)
	if err != nil {
		return err
	}
	collectorHash, err := security.HashPassword("collector123")
	if err != nil {
		return err
	}
	customerHash, err := security.HashPassword("customer123")
	if err != nil {
		return err
	}

	insertUser := func(name, email, hash, role string, phone, avatar *string, verified bool) (string, error) {
		id := store.NewID()
		_, err := tx.Exec(ctx, `INSERT INTO "User" (id, name, email, "passwordHash", role, phone, "avatarUrl", "isVerified", "createdAt")
			VALUES ($1, $2, $3, $4, $5::"Role", $6, $7, $8, $9)`, id, name, email, hash, role, phone, avatar, verified, store.Now())
		return id, err
	}
	setLoc := func(id string, lat, lng float64) error {
		_, err := tx.Exec(ctx, `UPDATE "User" SET location = ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography WHERE id = $3`, lng, lat, id)
		return err
	}
	strp := func(s string) *string { return &s }

	if _, err := insertUser("Admin Rongsok", "admin@rongsok.in", adminHash, store.RoleAdmin, nil, nil, false); err != nil {
		return err
	}

	// Pengepul (Budi buka, Eco tutup) di Yogyakarta.
	budi, err := insertUser("Budi Pengepul", "budi@collector.com", collectorHash, store.RoleCollector, strp("089876543210"), nil, true)
	if err != nil {
		return err
	}
	eco, err := insertUser("Pak Pengepul Eco", "eco.collector@example.com", collectorHash, store.RoleCollector, strp("089999888777"), nil, true)
	if err != nil {
		return err
	}
	for _, id := range []string{budi, eco} {
		if err := setLoc(id, -7.7956, 110.3695); err != nil {
			return err
		}
	}
	type shop struct {
		userID, name, desc string
		radius             float64
		open               bool
		minP, maxP         float64
	}
	for _, s := range []shop{
		{budi, "Lapak Budi Yogyakarta", "Pengepul Daur Ulang Terpercaya & Cepat", 25, true, 1000, 5000},
		{eco, "Lapak Eco Yogyakarta", "Mitra Daur Ulang Khusus Ramah Lingkungan", 15, false, 1500, 4500},
	} {
		profileID := store.NewID()
		if _, err := tx.Exec(ctx, `INSERT INTO "CollectorProfile" (id, "userId", "shopName", description, "radiusKm", "isOpen")
			VALUES ($1, $2, $3, $4, $5, $6)`, profileID, s.userID, s.name, s.desc, s.radius, s.open); err != nil {
			return err
		}
		for _, c := range categories {
			if _, err := tx.Exec(ctx, `INSERT INTO "CollectorCatalog" (id, "collectorId", "categoryId", "minPrice", "maxPrice", "isActive")
				VALUES ($1, $2, $3, $4, $5, true)`, store.NewID(), profileID, catIDs[c.name], s.minP, s.maxP); err != nil {
				return err
			}
		}
	}

	paper, plastic := catIDs["Kertas & Kardus"], catIDs["Plastik"]
	now := time.Now().UTC()
	for _, spec := range customers {
		phone := fmt.Sprintf("0812%d", 10000000+randInt(90000000))
		avatar := "https://api.dicebear.com/7.x/adventurer/svg?seed=" + url.QueryEscape(spec.name)
		id, err := insertUser(spec.name, spec.email, customerHash, store.RoleCustomer, &phone, &avatar, true)
		if err != nil {
			return err
		}
		// Titik acak ±0.0075° dari pusat Yogyakarta.
		if err := setLoc(id, -7.7956+(mrand.Float64()-0.5)*0.015, 110.3695+(mrand.Float64()-0.5)*0.015); err != nil {
			return err
		}

		if spec.weightKg > 0 {
			orderID := store.NewID()
			created := now.Add(-48 * time.Hour)
			total := spec.weightKg * 2000
			if _, err := tx.Exec(ctx, `INSERT INTO "Order" (id, "customerId", "collectorId", method, status, "estimatedWeight",
					"actualWeight", "agreedPrice", "totalPrice", "createdAt", "updatedAt")
				VALUES ($1, $2, $3, 'DROPOFF', 'COMPLETED', $4, $4, 2000, $5, $6, $7)`,
				orderID, id, budi, spec.weightKg, total, created, created.Add(time.Hour)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO "OrderItem" (id, "orderId", "categoryId", "estimatedWeight", "actualWeight", "agreedPrice", notes)
				VALUES ($1, $2, $3, $4, $4, 2000, 'Setoran kertas kardus daur ulang')`, store.NewID(), orderID, paper, spec.weightKg); err != nil {
				return err
			}
			if err := store.CreateReceipt(ctx, tx, orderID, map[string]any{
				"customer": id, "collector": budi, "total": total,
				"items": []map[string]any{{"category": "Kertas & Kardus", "estimatedWeight": spec.weightKg,
					"actualWeight": spec.weightKg, "agreedPrice": 2000, "total": total}},
			}); err != nil {
				return err
			}
		}
		if spec.hasCancelledOrd {
			orderID := store.NewID()
			if _, err := tx.Exec(ctx, `INSERT INTO "Order" (id, "customerId", method, status, "estimatedWeight", "createdAt", "updatedAt")
				VALUES ($1, $2, 'PICKUP', 'CANCELLED', 2, $3, $4)`, orderID, id, now.Add(-time.Hour), now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO "OrderItem" (id, "orderId", "categoryId", "estimatedWeight", notes)
				VALUES ($1, $2, $3, 2, 'Mencoba order jemput lalu batal')`, store.NewID(), orderID, plastic); err != nil {
				return err
			}
		}
	}
	slog.Info("seeder selesai", "categories", len(categories), "customers", len(customers),
		"akun", "admin@rongsok.in (SEED_ADMIN_PASSWORD), budi@collector.com / collector123, rizky@example.com / customer123")
	return nil
}

func randInt(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0
	}
	return v.Int64()
}
