package tests

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
)

func TestKYCAttestation(t *testing.T) {
	nik1 := mkNIK()
	u, r := register(t, "CUSTOMER", map[string]any{"nik": nik1, "ktpName": "BUDI"})
	if r.Status != 201 || r.data()["user"].(map[string]any)["isVerified"] != false {
		t.Fatalf("register NIK tanpa attestation harus dibuat tapi TIDAK terverifikasi: %d %s", r.Status, r.Raw)
	}
	if nik := queryVal[*string](t, `SELECT nik FROM "User" WHERE id = $1`, u.ID); nik != nil {
		t.Error("NIK tanpa bukti tidak boleh disimpan (anti serobot NIK)")
	}
	unverified := u.Token

	_, r = register(t, "CUSTOMER", map[string]any{"nik": nik1, "ktpName": "BUDI", "kycAttestation": signKYC(nik1, "BUDI")})
	if r.Status != 201 || r.data()["user"].(map[string]any)["isVerified"] != true {
		t.Errorf("register dengan attestation valid harus terverifikasi: %d %s", r.Status, r.Raw)
	}
	_, r = register(t, "CUSTOMER", map[string]any{"nik": nik1, "ktpName": "BUDI", "kycAttestation": signKYC(nik1, "BUDI")})
	expectStatus(t, r, 409, "NIK yang sama tidak bisa dipakai akun kedua")

	nik2 := mkNIK()
	_, r = register(t, "CUSTOMER", map[string]any{"nik": nik2, "kycAttestation": signKYC(mkNIK(), "X")})
	if r.Status != 201 || r.data()["user"].(map[string]any)["isVerified"] != false {
		t.Errorf("attestation untuk NIK lain → tidak terverifikasi: %d %s", r.Status, r.Raw)
	}

	att := signKYC(nik2, "BUDI")
	tampered := att[:len(att)-3] + "AAA"
	if strings.HasSuffix(att, "AAA") {
		tampered = att[:len(att)-3] + "BBB"
	}
	expectStatus(t, call(t, "PATCH", "/auth/me", unverified, map[string]any{"nik": nik2, "kycAttestation": tampered}), 422,
		"PATCH /me attestation dipalsukan")
	expectStatus(t, call(t, "PATCH", "/auth/me", unverified, map[string]any{"nik": nik2}), 422, "PATCH /me tanpa attestation")
	_, r = register(t, "CUSTOMER", map[string]any{"nik": "1234567890123456"})
	expectStatus(t, r, 422, "NIK asal ketik (tanggal tidak valid)")

	r = call(t, "PATCH", "/auth/me", unverified, map[string]any{"nik": nik2, "ktpName": "BUDI S", "kycAttestation": att})
	if r.Status != 200 || r.data()["isVerified"] != true || r.data()["ktpName"] != "BUDI S" {
		t.Errorf("verifikasi in-app via attestation: %d %s", r.Status, r.Raw)
	}
	nik3 := mkNIK()
	expectStatus(t, call(t, "PATCH", "/auth/me", unverified, map[string]any{"nik": nik3, "kycAttestation": signKYC(nik3, "B")}), 409,
		"akun terverifikasi tidak bisa ganti identitas")
}

func TestCollectorMustBeVerifiedToAccept(t *testing.T) {
	c := collector(t, -7.78, 110.37, false)
	cu := customer(t, -7.8, 110.36)
	cat := newCategory(t)
	r := call(t, "POST", "/orders", cu.Token, map[string]any{"method": "DROPOFF", "lat": -7.8, "lng": 110.36, "collectorId": c.ID,
		"items": []any{map[string]any{"categoryId": cat, "estimatedWeight": 2}}})
	expectStatus(t, r, 201, "buat order forward")
	oid := r.data()["id"].(string)
	expectStatus(t, call(t, "PATCH", "/orders/"+oid, c.Token, map[string]any{"action": "accept"}), 403, "pengepul belum verifikasi tidak bisa accept")
	nik := mkNIK()
	r = call(t, "PATCH", "/auth/me", c.Token, map[string]any{"nik": nik, "ktpName": "PENGEPUL", "kycAttestation": signKYC(nik, "PENGEPUL")})
	if r.Status != 200 || r.data()["isVerified"] != true {
		t.Fatalf("pengepul verifikasi via attestation: %d %s", r.Status, r.Raw)
	}
	expectStatus(t, call(t, "PATCH", "/orders/"+oid, c.Token, map[string]any{"action": "accept"}), 200, "setelah verifikasi, accept berhasil")
}

func multipartImage(t *testing.T, field, filename, contentType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
	h.Set("Content-Type", contentType)
	w, _ := mw.CreatePart(h)
	w.Write(data)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func upload(t *testing.T, token string, body *bytes.Buffer, ct string) resp {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(t, req)
}

func TestUpload(t *testing.T) {
	cu := mustRegister(t, "CUSTOMER")
	b, ct := multipartImage(t, "image", "a.png", "image/png", []byte("x"))
	expectStatus(t, upload(t, "", b, ct), 401, "upload tanpa login")
	b, ct = multipartImage(t, "image", "a.svg", "image/svg+xml", []byte("<svg/>"))
	expectStatus(t, upload(t, cu.Token, b, ct), 400, "upload SVG ditolak")
	b, ct = multipartImage(t, "image", "big.png", "image/png", bytes.Repeat([]byte("x"), 5<<20+10))
	r := upload(t, cu.Token, b, ct)
	if r.Status != 400 || !strings.Contains(r.Body["message"].(string), "5MB") {
		t.Errorf("file >5MB ditolak: %d %s", r.Status, r.Raw)
	}
	b, ct = multipartImage(t, "other", "a.png", "image/png", []byte("x"))
	expectStatus(t, upload(t, cu.Token, b, ct), 400, "tanpa field image → no file")
	// Cloudinary tidak dikonfigurasi di tes → 500 konfigurasi (bukan crash).
	b, ct = multipartImage(t, "image", "a.png", "image/png", []byte("x"))
	r = upload(t, cu.Token, b, ct)
	if r.Status != 500 || !strings.Contains(r.Body["message"].(string), "Cloudinary") {
		t.Errorf("cloudinary belum dikonfigurasi: %d %s", r.Status, r.Raw)
	}
}

func TestRateLimits(t *testing.T) {
	victim := mustRegister(t, "CUSTOMER")
	var codes []int
	for i := 0; i < 12; i++ {
		codes = append(codes, call(t, "POST", "/auth/login", "", map[string]any{"email": victim.Email, "password": "salahsalah"}).Status)
	}
	for i, c := range codes {
		want := 401
		if i >= 10 {
			want = 429
		}
		if c != want {
			t.Fatalf("login per email: 10x 401 lalu 429, dapat %v", codes)
		}
	}
	codes = nil
	for i := 0; i < 4; i++ {
		codes = append(codes, call(t, "POST", "/auth/forgot-password", "", map[string]any{"email": "nobody@t.io"},
			"CF-Connecting-IP", "203.0.113.9").Status)
	}
	if codes[2] != 200 || codes[3] != 429 {
		t.Errorf("forgot-password per email dibatasi: %v", codes)
	}
	cu := mustRegister(t, "CUSTOMER")
	for i := 0; i < 12; i++ {
		expectStatus(t, call(t, "PATCH", "/auth/me", cu.Token, map[string]any{"lat": -7.8, "lng": 110.36}), 200,
			"update lokasi biasa tidak kena limit KYC")
	}
	r := call(t, "POST", "/auth/login", "", map[string]any{"email": victim.Email, "password": "password123"},
		"CF-Connecting-IP", "198.51.100.1")
	expectStatus(t, r, 429, "email yang dikunci tetap 429 walau password benar")
}

func TestAuthBasics(t *testing.T) {
	u := mustRegister(t, "CUSTOMER")
	r := call(t, "POST", "/auth/login", "", map[string]any{"email": u.Email, "password": "password123"})
	expectStatus(t, r, 200, "login")
	if r.data()["access_token"] == "" {
		t.Error("login tanpa token")
	}
	_, r = register(t, "CUSTOMER", map[string]any{"email": u.Email})
	expectStatus(t, r, 400, "email terdaftar")
	_, r = register(t, "ADMIN", nil)
	expectStatus(t, r, 422, "tidak bisa daftar sebagai ADMIN")
	expectStatus(t, call(t, "GET", "/auth/me", "", nil), 401, "/me tanpa token")
	expectStatus(t, call(t, "GET", "/auth/me", "abc.def.ghi", nil), 401, "/me token rusak")
	r = call(t, "GET", "/auth/me", u.Token, nil)
	if r.Status != 200 || r.data()["email"] != u.Email || r.data()["lat"] != nil {
		t.Errorf("/me: %d %s", r.Status, r.Raw)
	}
	if _, leaked := r.data()["passwordHash"]; leaked {
		t.Error("/me membocorkan passwordHash")
	}
	exec(t, `DELETE FROM "User" WHERE id = $1`, u.ID)
	r = call(t, "GET", "/auth/me", u.Token, nil)
	if r.Status != 401 || r.Body["message"] != "Unauthorized, user no longer exists" {
		t.Errorf("user dihapus → 401: %d %s", r.Status, r.Raw)
	}
}

func TestResetPassword(t *testing.T) {
	u := mustRegister(t, "CUSTOMER")
	expectStatus(t, call(t, "POST", "/auth/forgot-password", "", map[string]any{"email": u.Email}), 200, "forgot-password terdaftar")
	if queryVal[*string](t, `SELECT "resetPasswordToken" FROM "User" WHERE id = $1`, u.ID) == nil {
		t.Fatal("token reset tidak tersimpan")
	}
	// Token mentah hanya dikirim via email — simulasikan dengan menimpa hash yang diketahui.
	exec(t, `UPDATE "User" SET "resetPasswordToken" = encode(sha256('rahasia'::bytea), 'hex') WHERE id = $1`, u.ID)
	expectStatus(t, call(t, "POST", "/auth/reset-password", "", map[string]any{"token": "salah", "password": "passwordbaru1"}), 400, "token salah")
	expectStatus(t, call(t, "POST", "/auth/reset-password", "", map[string]any{"token": "rahasia", "password": "pendek"}), 422, "password pendek")
	expectStatus(t, call(t, "POST", "/auth/reset-password", "", map[string]any{"token": "rahasia", "password": "passwordbaru1"}), 200, "reset ok")
	expectStatus(t, call(t, "POST", "/auth/reset-password", "", map[string]any{"token": "rahasia", "password": "passwordbaru2"}), 400, "token sekali pakai")
	expectStatus(t, call(t, "POST", "/auth/login", "", map[string]any{"email": u.Email, "password": "passwordbaru1"}), 200, "login password baru")
}
