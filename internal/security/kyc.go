package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// ── Attestation KYC ──────────────────────────────────────────────────────────
// OCR KTP yang sah jalan di server FE (Next route /api/ktp-ocr). Route itu
// menandatangani hasil bacaannya dengan HMAC-SHA256 memakai secret bersama
// KYC_ATTESTATION_SECRET. BE hanya memberi isVerified=true kalau tanda tangan
// valid, belum kedaluwarsa, dan NIK-nya cocok.
//
// Format token: base64url(JSON{ nik, nama, exp }) + "." + base64url(HMAC)
// (harus identik dengan rongsokin-web/lib/kycAttestation.ts)

type KYC struct{ secret []byte }

// NewKYC: secret kosong = mode legacy (NIK dari client dipercaya mentah).
func NewKYC(secret string) *KYC { return &KYC{secret: []byte(secret)} }

func (k *KYC) Enforced() bool { return len(k.secret) > 0 }

type Attestation struct {
	NIK  string
	Nama string
}

// VerifyAttestation mengembalikan isi token bila valid, atau nil.
func (k *KYC) VerifyAttestation(token string) *Attestation {
	if !k.Enforced() || token == "" || len(token) > 2048 {
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil
	}
	body, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, k.secret)
	mac.Write([]byte(body))
	expected := mac.Sum(nil)
	given, err := decodeB64URL(sig)
	if err != nil || !hmac.Equal(given, expected) {
		return nil
	}

	raw, err := decodeB64URL(body)
	if err != nil {
		return nil
	}
	var payload struct {
		NIK  any `json:"nik"`
		Nama any `json:"nama"`
		Exp  any `json:"exp"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	nik, ok := payload.NIK.(string)
	if !ok {
		return nil
	}
	exp, ok := payload.Exp.(float64)
	if !ok || exp < float64(time.Now().Unix()) {
		return nil
	}
	nama, _ := payload.Nama.(string)
	return &Attestation{NIK: nik, Nama: nama}
}

// Buffer.from(x, 'base64url') di Node toleran padding; samakan.
func decodeB64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// IsPlausibleNIK: 16 digit PP KK CC DDMMYY NNNN; tanggal lahir di digit 7–12,
// perempuan tanggal +40. Bukan bukti kepemilikan — cuma menyaring NIK asal ketik.
func IsPlausibleNIK(nik string) bool {
	if len(nik) != 16 {
		return false
	}
	for _, c := range nik {
		if c < '0' || c > '9' {
			return false
		}
	}
	if nik[0:2] == "00" || nik[2:4] == "00" || nik[12:] == "0000" {
		return false
	}
	day, _ := strconv.Atoi(nik[6:8])
	month, _ := strconv.Atoi(nik[8:10])
	if day > 40 {
		day -= 40
	}
	return day >= 1 && day <= 31 && month >= 1 && month <= 12
}

// KYCResult: NIK hanya diisi kalau terverifikasi (atau mode legacy) — NIK yang
// tak terbukti TIDAK disimpan, supaya tak bisa dipakai menyerobot identitas orang.
type KYCResult struct {
	Verified bool
	NIK      *string
	KTPName  *string
}

// Resolve menentukan hasil KYC dari input client.
func (k *KYC) Resolve(nik, ktpName, attestation string) KYCResult {
	if nik == "" {
		return KYCResult{}
	}
	if !k.Enforced() {
		return KYCResult{Verified: true, NIK: &nik, KTPName: nonEmpty(ktpName)}
	}
	att := k.VerifyAttestation(attestation)
	if att == nil || att.NIK != nik {
		return KYCResult{}
	}
	// Nama boleh dikoreksi user (OCR kadang salah eja); yang dikunci adalah NIK.
	name := ktpName
	if name == "" {
		name = att.Nama
	}
	return KYCResult{Verified: true, NIK: &nik, KTPName: nonEmpty(name)}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
