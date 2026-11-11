package tests

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func item(cat string, w float64) map[string]any {
	return map[string]any{"categoryId": cat, "estimatedWeight": w}
}

// TestWarOrderLifecycle: order war end-to-end + otorisasi + realtime.
func TestWarOrderLifecycle(t *testing.T) {
	cat := newCategory(t)
	A := customer(t, -7.8, 110.36)
	X := mustRegister(t, "CUSTOMER") // penyerang
	C1 := collector(t, -7.78, 110.37, true)
	C2 := collector(t, -7.79, 110.37, true)

	r := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": X.ID,
		"items": []any{item(cat, 3)}})
	expectStatus(t, r, 400, "forward ke non-pengepul ditolak")
	expectStatus(t, call(t, "POST", "/orders", C1.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36,
		"collectorId": C1.ID, "items": []any{item(cat, 3)}}), 400, "tidak bisa pesan ke lapak sendiri")
	expectStatus(t, call(t, "POST", "/orders", A.Token, map[string]any{"method": "PICKUP", "lat": -7.8, "lng": 110.36,
		"items": []any{item(cat, 3)}}), 403, "transaksi pertama wajib drop-off")

	// Pengepul online sebelum order dibuat → dapat new_order.
	sc1, _ := dial(t, C1.Token)
	sc1.send(t, "join_room", "collector:"+C1.ID)
	waitRoom(t, "collector:"+C1.ID, 1)

	r = call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "photoUrl": "https://x/p.jpg",
		"items": []any{item(cat, 3)}})
	expectStatus(t, r, 201, "customer buat order war")
	orderID := r.data()["id"].(string)
	if r.data()["collectorId"] != nil || len(r.data()["items"].([]any)) != 1 {
		t.Errorf("respons order war: %s", r.Raw)
	}
	if e := sc1.waitFor("new_order", 2*time.Second); e == nil {
		t.Error("pengepul tidak menerima new_order")
	} else {
		var p map[string]any
		json.Unmarshal(e.Data, &p)
		if p["orderId"] != orderID || p["collectorId"] != nil || p["photoUrl"] != "https://x/p.jpg" || p["estWeight"] != 3.0 {
			t.Errorf("payload new_order: %s", e.Data)
		}
	}

	if _, ok := dial(t, ""); ok {
		t.Error("socket tanpa token harus ditolak")
	}
	if _, ok := dial(t, "token.palsu.sekali"); ok {
		t.Error("socket token palsu harus ditolak")
	}
	sx, _ := dial(t, X.Token)
	sa, _ := dial(t, A.Token)
	sx.send(t, "join_room", "order:"+orderID)
	sx.send(t, "join_room", "collector:"+C1.ID)
	sx.send(t, "join_room", "customer:"+A.ID)
	sa.send(t, "join_room", "order:"+orderID)
	sc1.send(t, "join_room", "order:"+orderID)
	waitRoom(t, "order:"+orderID, 2)

	expectStatus(t, call(t, "GET", "/orders/"+orderID, X.Token, nil), 403, "attacker GET detail order")
	expectStatus(t, call(t, "GET", "/orders/"+orderID, C1.Token, nil), 200, "pengepul kandidat war bisa lihat detail")
	r = call(t, "GET", "/orders/"+orderID, A.Token, nil)
	cust := r.data()["customer"].(map[string]any)
	if _, leak := cust["passwordHash"]; leak || cust["nik"] != nil {
		t.Error("detail order membocorkan passwordHash/nik")
	}

	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, X.Token, map[string]any{"action": "accept"}), 403, "customer lain tidak bisa accept")

	// Dua pengepul accept bersamaan → tepat satu menang.
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, c := range []user{C1, C2} {
		wg.Add(1)
		go func(i int, c user) {
			defer wg.Done()
			statuses[i] = call(t, "PATCH", "/orders/"+orderID, c.Token, map[string]any{"action": "accept"}).Status
		}(i, c)
	}
	wg.Wait()
	if (statuses[0] == 200) == (statuses[1] == 200) {
		t.Fatalf("accept bersamaan harus tepat satu menang: %v", statuses)
	}
	winner, loser := C1, C2
	if statuses[1] == 200 {
		winner, loser = C2, C1
	}
	if e := sa.waitFor("order_status_update", 2*time.Second); e == nil {
		t.Error("customer tidak menerima order_status_update setelah accept")
	}
	if e := sc1.waitFor("order_taken", 2*time.Second); e == nil {
		t.Error("pengepul kandidat tidak menerima order_taken")
	}

	sw := sc1
	if winner.ID != C1.ID {
		sw, _ = dial(t, winner.Token)
		sw.send(t, "join_room", "order:"+orderID)
		waitRoom(t, "order:"+orderID, 3)
	}
	sw.send(t, "location_update", map[string]any{"orderId": orderID, "lat": -7.79, "lng": 110.37, "role": "X"})
	if e := sa.waitFor("location_update", 2*time.Second); e == nil {
		t.Error("customer tidak menerima live location dari pengepul")
	} else {
		var p map[string]any
		json.Unmarshal(e.Data, &p)
		if p["lat"] != -7.79 || p["role"] != "COLLECTOR" || p["userId"] != winner.ID {
			t.Errorf("payload location_update: %s", e.Data)
		}
	}
	sx.send(t, "location_update", map[string]any{"orderId": orderID, "lat": 1, "lng": 1})
	if e := sa.waitFor("location_update", 300*time.Millisecond); e != nil {
		t.Error("attacker bisa mengirim lokasi palsu")
	}

	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, A.Token, map[string]any{"action": "arrive"}), 200, "arrive oleh customer")
	itemID := queryVal[string](t, `SELECT id FROM "OrderItem" WHERE "orderId" = $1`, orderID)
	weigh := func(id string, w float64) map[string]any {
		return map[string]any{"action": "validate", "items": []any{map[string]any{"id": id, "actualWeight": w, "agreedPrice": 2000}}}
	}
	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, A.Token, weigh(itemID, 3)), 403, "customer tidak bisa validate")
	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, loser.Token, weigh(itemID, 3)), 403, "pengepul yang kalah tidak bisa validate")
	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, winner.Token, weigh(itemID, -3)), 422, "berat negatif ditolak")
	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, winner.Token, weigh("00000000-0000-0000-0000-000000000000", 3)), 422,
		"item order lain ditolak")
	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, winner.Token, weigh(itemID, 3)), 200, "pengepul pemenang validate")
	expectStatus(t, call(t, "PATCH", "/orders/"+orderID, winner.Token, map[string]any{"action": "confirm"}), 403, "pengepul tidak bisa confirm")

	statuses = make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = call(t, "PATCH", "/orders/"+orderID, A.Token, map[string]any{"action": "confirm"}).Status
		}(i)
	}
	wg.Wait()
	if !((statuses[0] == 200) != (statuses[1] == 200)) || statuses[0] == 500 || statuses[1] == 500 {
		t.Errorf("double confirm: satu sukses, tanpa 500: %v", statuses)
	}
	status := queryVal[string](t, `SELECT status::text FROM "Order" WHERE id = $1`, orderID)
	total := queryVal[float64](t, `SELECT "totalPrice" FROM "Order" WHERE id = $1`, orderID)
	receipt := queryVal[map[string]any](t, `SELECT "detailsJson" FROM "Receipt" WHERE "orderId" = $1`, orderID)
	if status != "COMPLETED" || total != 6000 || receipt["total"] != 6000.0 || len(receipt["items"].([]any)) != 1 {
		t.Errorf("order COMPLETED, total 6000, receipt: %s %v %v", status, total, receipt)
	}

	expectStatus(t, call(t, "POST", "/ratings", X.Token, map[string]any{"orderId": orderID, "rateeId": winner.ID, "score": 1}), 403, "attacker tidak bisa rating")
	expectStatus(t, call(t, "POST", "/ratings", A.Token, map[string]any{"orderId": orderID, "rateeId": X.ID, "score": 1}), 400, "rating ke pihak luar")
	expectStatus(t, call(t, "POST", "/ratings", A.Token, map[string]any{"orderId": orderID, "rateeId": winner.ID, "score": 4.5}), 422, "score pecahan")
	expectStatus(t, call(t, "POST", "/ratings", A.Token, map[string]any{"orderId": orderID, "rateeId": winner.ID, "score": 5}), 201, "customer rating pengepul")
	expectStatus(t, call(t, "POST", "/ratings", A.Token, map[string]any{"orderId": orderID, "rateeId": winner.ID, "score": 5}), 409, "rating ganda")
	expectStatus(t, call(t, "POST", "/ratings", winner.Token, map[string]any{"orderId": orderID, "rateeId": A.ID, "score": 4}), 201, "pengepul rating customer")
	if avg := queryVal[float64](t, `SELECT "avgRating" FROM "User" WHERE id = $1`, winner.ID); avg != 5 {
		t.Errorf("avgRating pengepul = %v, want 5", avg)
	}
	r = call(t, "GET", "/ratings/user/"+winner.ID, A.Token, nil)
	if len(r.list()) != 1 || r.list()[0].(map[string]any)["rater"].(map[string]any)["id"] != A.ID {
		t.Errorf("GET ratings user: %s", r.Raw)
	}

	if evs := sx.drain(200 * time.Millisecond); len(evs) != 0 {
		t.Errorf("attacker menerima event dari room orang lain: %v", evs)
	}
	expectStatus(t, call(t, "POST", "/auth/forgot-password", "", map[string]any{"email": "tidakada@t.io"}), 200, "forgot-password generik")

	// Setelah 1 transaksi selesai, PICKUP terbuka.
	expectStatus(t, call(t, "POST", "/orders", A.Token, map[string]any{"method": "PICKUP", "lat": -7.8, "lng": 110.36,
		"categoryId": cat, "estimatedWeight": 1.5}), 201, "pickup setelah 1 transaksi selesai")
}

func TestForwardQueueRejectCancel(t *testing.T) {
	cat := newCategory(t)
	A := customer(t, -7.8, 110.36)
	C := collector(t, -7.78, 110.37, true)

	r := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": C.ID,
		"categoryId": cat, "estimatedWeight": 2, "notes": "kardus"})
	expectStatus(t, r, 201, "order forward (legacy single-category)")
	fwdID := r.data()["id"].(string)
	if r.data()["categoryId"] != cat || r.data()["collectorId"] != C.ID {
		t.Errorf("order forward: %s", r.Raw)
	}
	expectStatus(t, call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36}), 422,
		"tanpa items maupun categoryId")

	queue := call(t, "GET", "/orders?role=collector&status=PENDING", C.Token, nil).list()
	found := false
	for _, o := range queue {
		if o.(map[string]any)["id"] == fwdID {
			found = true
		}
	}
	if !found {
		t.Fatal("order forward tidak muncul di antrean pengepul")
	}
	r = call(t, "PATCH", "/orders/"+fwdID, C.Token, map[string]any{"action": "reject"})
	if r.Status != 200 || r.data()["rejected"] != true || r.data()["status"] != "PENDING" {
		t.Errorf("reject: %s", r.Raw)
	}
	for _, o := range call(t, "GET", "/orders?role=collector&status=PENDING", C.Token, nil).list() {
		if o.(map[string]any)["id"] == fwdID {
			t.Error("order yang ditolak masih di antrean")
		}
	}
	expectStatus(t, call(t, "PATCH", "/orders/"+fwdID, A.Token, map[string]any{"action": "cancel"}), 200, "customer batal")
	expectStatus(t, call(t, "PATCH", "/orders/"+fwdID, A.Token, map[string]any{"action": "cancel"}), 400, "batal dua kali")
	expectStatus(t, call(t, "PATCH", "/orders/"+fwdID, A.Token, map[string]any{"action": "terbang"}), 400, "aksi tak dikenal")
	expectStatus(t, call(t, "PATCH", "/orders/00000000-0000-0000-0000-000000000000", A.Token, map[string]any{"action": "cancel"}), 404, "order tak ada")
	r = call(t, "GET", "/orders/00000000-0000-0000-0000-000000000000", A.Token, nil)
	if r.Status != 200 || r.Body["data"] != nil {
		t.Errorf("detail order tak ada → 200 data null: %s", r.Raw)
	}

	// Batas pesanan aktif pengepul.
	exec(t, `UPDATE "CollectorProfile" SET "maxConcurrentOrders" = 1 WHERE "userId" = $1`, C.ID)
	ids := []string{}
	for range 2 {
		r := call(t, "POST", "/orders", A.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": C.ID,
			"items": []any{item(cat, 1)}})
		ids = append(ids, r.data()["id"].(string))
	}
	expectStatus(t, call(t, "PATCH", "/orders/"+ids[0], C.Token, map[string]any{"action": "accept"}), 200, "accept pertama")
	expectStatus(t, call(t, "PATCH", "/orders/"+ids[1], C.Token, map[string]any{"action": "accept"}), 400, "melebihi batas pesanan aktif")
}
