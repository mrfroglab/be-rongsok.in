package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestPlausibleNIK(t *testing.T) {
	cases := map[string]bool{
		"3404011508950001": true,  // laki-laki 15-08-1995
		"3404015508950001": true,  // perempuan (15+40)
		"1234567890123456": false, // tanggal 78
		"0004011508950001": false, // provinsi 00
		"3400011508950001": false, // kabupaten 00
		"3404011508950000": false, // nomor urut 0000
		"3404011513950001": false, // bulan 13
		"340401150895000":  false, // 15 digit
		"34040115089500a1": false,
	}
	for nik, want := range cases {
		if got := IsPlausibleNIK(nik); got != want {
			t.Errorf("IsPlausibleNIK(%s) = %v, want %v", nik, got, want)
		}
	}
}

// Token dari jsonwebtoken (Node) untuk payload {id, role, iat, exp} harus
// diterima — sesi user lama tetap valid setelah migrasi ke Go.
func TestVerifyNodeIssuedToken(t *testing.T) {
	secret := "s3cr3t-s3cr3t-s3cr3t-s3cr3t-s3cr3t"
	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"id":"6f1c2b9e-0000-4000-8000-000000000001","role":"CUSTOMER","iat":` + itoa(now) + `,"exp":` + itoa(now+3600) + `}`))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(header + "." + payload))
	token := header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	id, err := NewTokens(secret).Verify(token)
	if err != nil || id != "6f1c2b9e-0000-4000-8000-000000000001" {
		t.Fatalf("token Node ditolak: %v", err)
	}
	if _, err := NewTokens(secret + "x").Verify(token); err == nil {
		t.Error("token dengan secret lain harus ditolak")
	}
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + payload + "."
	if _, err := NewTokens(secret).Verify(none); err == nil {
		t.Error("alg none harus ditolak")
	}
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	tk := NewTokens("another-secret-another-secret-0123")
	tok, err := tk.Issue("user-1", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := tk.Verify(tok); err != nil || id != "user-1" {
		t.Fatalf("round trip: %v %v", id, err)
	}
}

func TestKYCResolve(t *testing.T) {
	secret := "kyc-secret-kyc-secret-kyc-secret-01"
	sign := func(body string) string {
		b := base64.RawURLEncoding.EncodeToString([]byte(body))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(b))
		return b + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	k := NewKYC(secret)
	exp := itoa(time.Now().Unix() + 600)
	good := sign(`{"nik":"3404011508950001","nama":"BUDI","exp":` + exp + `}`)

	if r := k.Resolve("3404011508950001", "", good); !r.Verified || *r.KTPName != "BUDI" {
		t.Errorf("attestation valid: %+v", r)
	}
	if r := k.Resolve("3404011508950001", "BUDI S", good); *r.KTPName != "BUDI S" {
		t.Error("nama boleh dikoreksi user")
	}
	if r := k.Resolve("3404011508950002", "", good); r.Verified || r.NIK != nil {
		t.Error("NIK beda dengan attestation harus ditolak & tidak disimpan")
	}
	expired := sign(`{"nik":"3404011508950001","nama":"BUDI","exp":1}`)
	if r := k.Resolve("3404011508950001", "", expired); r.Verified {
		t.Error("attestation kedaluwarsa harus ditolak")
	}
	if r := k.Resolve("3404011508950001", "", good+"x"); r.Verified {
		t.Error("tanda tangan rusak harus ditolak")
	}
	legacy := NewKYC("")
	if r := legacy.Resolve("3404011508950001", "B", ""); !r.Verified {
		t.Error("mode legacy mempercayai NIK client")
	}
	if r := k.Resolve("", "", ""); r.Verified || r.NIK != nil {
		t.Error("tanpa NIK = tidak terverifikasi")
	}
}

func itoa(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
