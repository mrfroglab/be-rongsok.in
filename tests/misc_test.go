package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/migrations"
)

func TestSweeperCancelsStaleOrders(t *testing.T) {
	cat := newCategory(t)
	A := customer(t, -7.8, 110.36)
	C := collector(t, -7.78, 110.37, true)
	sc, _ := dial(t, C.Token)
	sc.send(t, "join_room", "collector:"+C.ID)
	waitRoom(t, "collector:"+C.ID, 1)

	war := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "items": []any{item(cat, 1)}}).data()["id"].(string)
	fwd := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": C.ID,
		"items": []any{item(cat, 1)}}).data()["id"].(string)
	otw := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": C.ID,
		"items": []any{item(cat, 1)}}).data()["id"].(string)
	expectStatus(t, call(t, "PATCH", "/orders/"+otw, C.Token, map[string]any{"action": "accept"}), 200, "accept")
	sc.drain(200 * time.Millisecond)

	exec(t, `UPDATE "Order" SET "createdAt" = now() - interval '16 minutes' WHERE id = ANY($1)`, []string{war, fwd})
	exec(t, `UPDATE "Order" SET "updatedAt" = now() - interval '3 hours 1 minute' WHERE id = $1`, otw)
	testApp.Sweeper.Sweep(context.Background())

	st := func(id string) string {
		return queryVal[string](t, `SELECT status::text FROM "Order" WHERE id = $1`, id)
	}
	if st(war) != "CANCELLED" {
		t.Error("order war >15 menit harus dibatalkan")
	}
	if st(fwd) != "PENDING" {
		t.Error("order forward tidak boleh kena batas 15 menit")
	}
	if st(otw) != "CANCELLED" {
		t.Error("order ON_THE_WAY >3 jam harus dibatalkan")
	}
	if e := sc.waitFor("order_taken", 2*time.Second); e == nil || !strings.Contains(string(e.Data), war) {
		t.Error("pengepul tidak menerima order_taken untuk war yang kedaluwarsa")
	}
}

func TestListAndDetailShapes(t *testing.T) {
	cat := newCategory(t)
	A := customer(t, -7.8, 110.36)
	C := collector(t, -7.78, 110.37, true)
	id := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": C.ID,
		"items": []any{item(cat, 2), item(cat, 1.5)}}).data()["id"].(string)

	list := call(t, "GET", "/orders", A.Token, nil).list()
	if len(list) != 1 {
		t.Fatalf("customer harus punya 1 order, dapat %d", len(list))
	}
	o := list[0].(map[string]any)
	for _, k := range []string{"id", "customerId", "collectorId", "categoryId", "method", "status", "createdAt", "updatedAt",
		"estimatedWeight", "customer", "collector", "category", "items", "customerLat", "customerLng", "collectorLat", "collectorLng", "distanceKm"} {
		if _, ok := o[k]; !ok {
			t.Errorf("list order tanpa field %q", k)
		}
	}
	if o["estimatedWeight"] != 3.5 || o["categoryId"] != nil || len(o["items"].([]any)) != 2 {
		t.Errorf("multi-item: %v", o)
	}
	if shop := o["collector"].(map[string]any)["collectorProfile"].(map[string]any); !strings.HasPrefix(shop["shopName"].(string), "Lapak ") {
		t.Errorf("collector.collectorProfile.shopName: %v", shop)
	}
	if d, _ := o["distanceKm"].(float64); d < 2 || d > 3 {
		t.Errorf("distanceKm tidak masuk akal: %v", o["distanceKm"])
	}
	if ts := o["createdAt"].(string); len(ts) != 24 || !strings.HasSuffix(ts, "Z") {
		t.Errorf("format waktu harus ISO JS (…000Z): %s", ts)
	}

	d := call(t, "GET", "/orders/"+id, C.Token, nil).data()
	if _, ok := d["distanceKm"]; ok {
		t.Error("detail tidak punya distanceKm")
	}
	if d["collector"].(map[string]any)["collectorProfile"].(map[string]any)["maxConcurrentOrders"] != 5.0 {
		t.Errorf("detail: collectorProfile lengkap: %v", d["collector"])
	}
	if d["ratings"] == nil || d["items"].([]any)[0].(map[string]any)["category"] == nil {
		t.Errorf("detail: ratings & items.category: %v", d)
	}

	limit := call(t, "GET", "/orders?limit=abc", A.Token, nil)
	expectStatus(t, limit, 200, "limit tidak valid → default")
	expectStatus(t, call(t, "GET", "/orders?status=NGAWUR", A.Token, nil), 200, "status ngawur → kosong, bukan 500")
}

func TestDiscoveryAndCollectorProfile(t *testing.T) {
	parent := newCategory(t)
	child := queryVal[string](t, `INSERT INTO "WasteCategory" (id, name, "parentId") VALUES (gen_random_uuid()::text, $1, $2) RETURNING id`,
		fmt.Sprintf("Anak%d", time.Now().UnixNano()), parent)
	C := collector(t, -6.2, 106.8, true) // Jakarta — jauh dari data tes lain
	r := call(t, "PATCH", "/collector/catalogs", C.Token, []any{
		map[string]any{"categoryId": child, "minPrice": 1000, "maxPrice": 4000},
	})
	expectStatus(t, r, 200, "update katalog")
	expectStatus(t, call(t, "PATCH", "/collector/catalogs", C.Token, []any{map[string]any{"categoryId": "nope", "minPrice": 1, "maxPrice": 2}}), 400,
		"kategori tidak ada")
	expectStatus(t, call(t, "PATCH", "/collector/catalogs", C.Token, map[string]any{"x": 1}), 422, "bukan array")

	r = call(t, "GET", "/discovery/search?lat=-6.2&lng=106.8&radius=1&categoryId="+parent, "", nil)
	if r.Status != 200 || len(r.list()) != 1 {
		t.Fatalf("search dengan kategori induk harus menemukan pengepul: %s", r.Raw)
	}
	hit := r.list()[0].(map[string]any)
	if hit["maxPrice"] != 4000.0 || hit["ownerVerified"] != true || hit["distance"].(float64) > 1 {
		t.Errorf("hasil search: %v", hit)
	}
	expectStatus(t, call(t, "GET", "/discovery/search?lat=-6.2", "", nil), 400, "lng wajib")
	expectStatus(t, call(t, "GET", "/discovery/search?lat=abc&lng=1", "", nil), 400, "lat bukan angka")

	profileID := hit["id"].(string)
	d := call(t, "GET", "/discovery/collectors/"+profileID, "", nil).data()
	if d["user"].(map[string]any)["lat"] != -6.2 || len(d["catalogs"].([]any)) != 1 {
		t.Errorf("detail lapak: %v", d)
	}
	expectStatus(t, call(t, "GET", "/discovery/collectors/nope", "", nil), 404, "lapak tak ada")

	r = call(t, "PATCH", "/collector/profile", C.Token, map[string]any{"shopName": "Lapak Baru", "isOpen": false})
	if r.data()["isOpen"] != false || r.data()["shopName"] != "Lapak Baru" || r.data()["radiusKm"] != 5.0 {
		t.Errorf("update profil parsial: %s", r.Raw)
	}
	if n := len(call(t, "GET", "/discovery/search?lat=-6.2&lng=106.8&radius=1", "", nil).list()); n != 0 {
		t.Error("lapak tutup tidak boleh muncul di pencarian")
	}
	cu := mustRegister(t, "CUSTOMER")
	expectStatus(t, call(t, "POST", "/collector/profile", cu.Token, map[string]any{"shopName": "Lapak Palsu"}), 403, "customer tak boleh buat lapak")
	expectStatus(t, call(t, "GET", "/collector/profile", cu.Token, nil), 404, "customer tak punya profil")

	for _, p := range []string{"/discovery/categories", "/discovery/stats", "/discovery/leaderboard?userId=" + cu.ID} {
		expectStatus(t, call(t, "GET", p, "", nil), 200, p)
	}
	me := call(t, "GET", "/discovery/leaderboard?userId="+cu.ID, "", nil).data()["me"].(map[string]any)
	if me["rank"] != nil || me["totalKg"] != 0.0 {
		t.Errorf("leaderboard me untuk user tanpa transaksi: %v", me)
	}
}

func TestAdmin(t *testing.T) {
	adminID := queryVal[string](t, `INSERT INTO "User" (id, name, email, "passwordHash", role)
		VALUES (gen_random_uuid()::text, 'Admin', $1, 'x', 'ADMIN') RETURNING id`, uniqueEmail("admin"))
	token, err := testTokens().Issue(adminID, "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	cu := mustRegister(t, "CUSTOMER")
	expectStatus(t, call(t, "GET", "/admin/stats", cu.Token, nil), 403, "non-admin")

	s := call(t, "GET", "/admin/stats", token, nil).data()
	if len(s["weeklyTransactions"].([]any)) != 7 {
		t.Errorf("weeklyTransactions harus 7 hari: %v", s)
	}
	r := call(t, "GET", "/admin/orders?page=1&limit=5", token, nil)
	if r.Status != 200 || r.data()["limit"] != 5.0 || r.data()["page"] != 1.0 {
		t.Errorf("admin orders: %s", r.Raw)
	}

	name := fmt.Sprintf("Kategori %d", time.Now().UnixNano())
	r = call(t, "POST", "/admin/categories", token, map[string]any{"name": name, "unit": "pcs", "sortOrder": 3})
	expectStatus(t, r, 201, "buat kategori")
	id := r.data()["id"].(string)
	expectStatus(t, call(t, "POST", "/admin/categories", token, map[string]any{"name": name}), 400, "nama kembar")
	expectStatus(t, call(t, "POST", "/admin/categories", token, map[string]any{"name": "Zz", "unit": "ton"}), 422, "unit tak valid")
	child := call(t, "POST", "/admin/categories", token, map[string]any{"name": name + " anak", "parentId": id}).data()["id"].(string)
	expectStatus(t, call(t, "DELETE", "/admin/categories/"+id, token, nil), 400, "induk masih punya anak")
	r = call(t, "PATCH", "/admin/categories/"+child, token, map[string]any{"parentId": nil, "description": "d"})
	if r.Status != 200 || r.data()["parentId"] != nil || r.data()["description"] != "d" {
		t.Errorf("lepas parent: %s", r.Raw)
	}
	expectStatus(t, call(t, "PATCH", "/admin/categories/00000000-0000-0000-0000-000000000000", token, map[string]any{"name": "Xyz"}), 404, "update tak ada")
	expectStatus(t, call(t, "DELETE", "/admin/categories/"+id, token, nil), 200, "hapus kategori")
	expectStatus(t, call(t, "DELETE", "/admin/categories/"+id, token, nil), 404, "hapus dua kali")
}

// TestMigrateAdoptsLegacyHistory: DB produksi lama (Prisma migrate / db push)
// harus dikenali tanpa menjalankan ulang baseline.
func TestMigrateAdoptsLegacyHistory(t *testing.T) {
	ctx := context.Background()
	baseline, _ := migrations.FS.ReadFile("20261110000000_baseline.sql")
	for _, tc := range []struct {
		name   string
		prisma bool
	}{{"prisma", true}, {"dbpush", false}} {
		t.Run(tc.name, func(t *testing.T) {
			dbName := fmt.Sprintf("adopt_%s_%d", tc.name, time.Now().UnixNano())
			exec(t, "CREATE DATABASE "+dbName)
			defer exec(t, "DROP DATABASE "+dbName+" WITH (FORCE)")
			u, _ := url.Parse(dbURL)
			u.Path = "/" + dbName
			p, err := pgxpool.New(ctx, u.String())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err := p.Exec(ctx, string(baseline)); err != nil {
				t.Fatal(err)
			}
			if tc.prisma {
				if _, err := p.Exec(ctx, `CREATE TABLE _prisma_migrations (id text, migration_name text, finished_at timestamptz, rolled_back_at timestamptz);
					INSERT INTO _prisma_migrations VALUES ('1', '20261110000000_baseline', now(), NULL)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Migrate(ctx, p, migrations.FS); err != nil {
				t.Fatalf("migrate DB lama: %v", err)
			}
			var versions []string
			rows, _ := p.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
			versions, _ = pgx.CollectRows(rows, pgx.RowTo[string])
			if strings.Join(versions, ",") != "20261110000000_baseline,20261110000100_user_location_gist,20261111000000_order_updated_at_trigger" {
				t.Errorf("versi tercatat: %v", versions)
			}
			var idx bool
			p.QueryRow(ctx, `SELECT to_regclass('"User_location_idx"') IS NOT NULL`).Scan(&idx)
			if !idx {
				t.Error("migrasi lanjutan (index GIST) tidak diterapkan")
			}
			if err := db.Migrate(ctx, p, migrations.FS); err != nil {
				t.Errorf("migrate kedua harus no-op: %v", err)
			}
		})
	}
}

func TestHealthAndDocs(t *testing.T) {
	for path, want := range map[string]string{"/health": `"status":"ok"`, "/api-docs": "swagger-ui", "/api-docs/swagger.yaml": "openapi"} {
		r := do(t, mustReq(t, "GET", srv.URL+path))
		if r.Status != 200 || !strings.Contains(string(r.Raw), want) {
			t.Errorf("%s: %d", path, r.Status)
		}
	}
	r := call(t, "GET", "/tidak-ada", "", nil)
	if r.Status != 404 || r.Body["status"] != "error" {
		t.Errorf("404 JSON: %s", r.Raw)
	}
	var v any
	if json.Unmarshal(r.Raw, &v) != nil {
		t.Error("404 bukan JSON")
	}
}

func TestOrderUpdatedAtTrigger(t *testing.T) {
	cat := newCategory(t)
	A := customer(t, -7.8, 110.36)
	r := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "items": []any{item(cat, 1)}})
	expectStatus(t, r, 201, "buat order")
	id := r.data()["id"].(string)
	exec(t, `UPDATE "Order" SET "updatedAt" = '2000-01-01' WHERE id = $1`, id)
	if y := queryVal[float64](t, `SELECT extract(year FROM "updatedAt") FROM "Order" WHERE id = $1`, id); y != 2000 {
		t.Errorf("nilai updatedAt eksplisit harus dihormati, dapat tahun %v", y)
	}
	exec(t, `UPDATE "Order" SET "photoUrl" = 'x' WHERE id = $1`, id)
	if y := queryVal[float64](t, `SELECT extract(year FROM "updatedAt") FROM "Order" WHERE id = $1`, id); y == 2000 {
		t.Error("trigger tidak memperbarui updatedAt pada UPDATE tanpa updatedAt")
	}
}
