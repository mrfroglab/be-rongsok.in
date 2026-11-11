package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
	"github.com/mrfroglab/be-rongsok.in/internal/middleware"
	"github.com/mrfroglab/be-rongsok.in/internal/security"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

type authUserJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Email      string  `json:"email"`
	Phone      *string `json:"phone"`
	Role       string  `json:"role"`
	AvatarURL  *string `json:"avatarUrl"`
	IsVerified bool    `json:"isVerified"`
}

func (h *Handler) tokenResponse(w http.ResponseWriter, r *http.Request, status int, u *store.UserRecord) {
	token, err := h.Tokens.Issue(u.ID, u.Role)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, status, map[string]any{
		"access_token": token,
		"user": authUserJSON{ID: u.ID, Name: u.Name, Email: u.Email, Phone: u.Phone, Role: u.Role,
			AvatarURL: u.AvatarURL, IsVerified: u.IsVerified},
	})
}

// validateKYCFields: NIK 16 digit & masuk akal, nama KTP 2–100, attestation ≤2048.
func validateKYCFields(v *httpx.V, nik, ktpName, att *string) {
	if nik != nil {
		if len(*nik) != 16 || strings.Trim(*nik, "0123456789") != "" {
			v.Add("nik", "NIK harus 16 digit")
		} else if !security.IsPlausibleNIK(*nik) {
			v.Add("nik", "NIK tidak valid")
		}
	}
	v.StrLen("ktpName", ktpName, 2, 100)
	v.StrLen("kycAttestation", att, -1, 2048)
}

func validatePassword(v *httpx.V, pw *string) {
	if httpx.Required(v, "password", pw) {
		v.StrLen("password", pw, 8, -1)
		if len(*pw) > security.MaxPasswordBytes {
			v.Add("password", "Password maksimal 72 byte")
		}
	}
}

// POST /auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Email, Password, Role   *string
		Phone, AvatarURL, AddressText *string
		NIK, KTPName                  *string
		KYCAttestation                *string `json:"kycAttestation"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	if httpx.Required(v, "name", in.Name) {
		v.StrLen("name", in.Name, 2, -1)
	}
	if httpx.Required(v, "email", in.Email) {
		v.Email("email", in.Email)
	}
	validatePassword(v, in.Password)
	if httpx.Required(v, "role", in.Role) {
		v.OneOf("role", in.Role, store.RoleCustomer, store.RoleCollector)
	}
	v.StrLen("addressText", in.AddressText, -1, 500)
	validateKYCFields(v, in.NIK, in.KTPName, in.KYCAttestation)
	if v.Fail(w) {
		return
	}

	ctx := r.Context()
	existing, err := store.GetUserByEmail(ctx, h.DB, *in.Email)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if existing != nil {
		httpx.Error(w, http.StatusBadRequest, "Email already registered")
		return
	}

	// KYC anti wash-trading: 1 KTP = 1 akun = 1 role. NIK adalah kunci unik utama.
	kyc := h.KYC.Resolve(str(in.NIK), str(in.KTPName), str(in.KYCAttestation))
	if kyc.NIK != nil {
		if dup, err := h.identityTaken(r, *kyc.NIK, kyc.KTPName, *in.Name, ""); err != nil {
			httpx.Internal(w, r, err)
			return
		} else if dup {
			httpx.Error(w, http.StatusConflict, "Identitas sudah terdaftar.")
			return
		}
	}

	hash, err := security.HashPassword(*in.Password)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Terverifikasi hanya bila NIK terbukti dari OCR server (attestation).
	// Foto KTP sengaja tidak disimpan demi privasi (ktpUrl dari client diabaikan).
	u, err := store.CreateUser(ctx, h.DB, store.NewUser{
		Name: *in.Name, Email: *in.Email, PasswordHash: hash, Role: *in.Role,
		Phone: in.Phone, AvatarURL: in.AvatarURL, AddressText: nonEmpty(in.AddressText),
		NIK: kyc.NIK, KTPName: kyc.KTPName, IsVerified: kyc.Verified,
	})
	if db.IsUniqueViolation(err) {
		// Unique constraint (email / NIK) kena saat request bersamaan.
		httpx.Error(w, http.StatusConflict, "Identitas atau email sudah terdaftar.")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.tokenResponse(w, r, http.StatusCreated, u)
}

// identityTaken: NIK sudah dipakai akun lain? Di mode legacy (NIK tak terbukti)
// juga cek nama KTP kembar. Kalau NIK dibuktikan lewat attestation, nama sama ≠
// orang sama (banyak "MUHAMMAD RIZKI"), jadi cek nama dilewati.
func (h *Handler) identityTaken(r *http.Request, nik string, ktpName *string, fallbackName, excludeID string) (bool, error) {
	ctx := r.Context()
	taken, err := store.NIKTaken(ctx, h.DB, nik, excludeID)
	if err != nil || taken {
		return taken, err
	}
	if h.KYC.Enforced() {
		return false, nil
	}
	ref := strings.TrimSpace(str(ktpName))
	if ref == "" {
		ref = strings.TrimSpace(fallbackName)
	}
	if ref == "" {
		return false, nil
	}
	return store.KTPNameTaken(ctx, h.DB, ref, excludeID)
}

// POST /auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password *string }
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !h.Limiter.Allow(w, middleware.RuleLoginEmail, middleware.EmailKey(str(in.Email))) {
		return
	}
	v := &httpx.V{}
	if httpx.Required(v, "email", in.Email) {
		v.Email("email", in.Email)
	}
	httpx.Required(v, "password", in.Password)
	if v.Fail(w) {
		return
	}
	u, err := store.GetUserByEmail(r.Context(), h.DB, *in.Email)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if u == nil || !security.CheckPassword(*in.Password, u.PasswordHash) {
		httpx.Error(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}
	h.tokenResponse(w, r, http.StatusOK, u)
}

// GET /auth/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	me, err := store.GetMe(r.Context(), h.DB, middleware.User(r).ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, me)
}

// PATCH /auth/me — update profil + lokasi + (opsional) verifikasi KTP.
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	user := middleware.User(r)
	var in struct {
		Name, Phone, AvatarURL, AddressText *string
		Lat, Lng                            *float64
		NIK, KTPName                        *string
		KYCAttestation                      *string `json:"kycAttestation"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	// Hanya PATCH yang membawa NIK (percobaan verifikasi) yang dihitung.
	if str(in.NIK) != "" && !h.Limiter.Allow(w, middleware.RuleKYC, user.ID) {
		return
	}
	v := &httpx.V{}
	v.StrLen("name", in.Name, 2, -1)
	v.StrLen("addressText", in.AddressText, -1, 500)
	v.Range("lat", in.Lat, -90, 90)
	v.Range("lng", in.Lng, -180, 180)
	validateKYCFields(v, in.NIK, in.KTPName, in.KYCAttestation)
	if v.Fail(w) {
		return
	}

	ctx := r.Context()
	sets := &store.Sets{}
	if in.Name != nil {
		sets.Add("name", *in.Name)
	}
	if in.Phone != nil {
		sets.Add("phone", *in.Phone)
	}
	if in.AvatarURL != nil {
		sets.Add("avatarUrl", *in.AvatarURL)
	}
	if in.AddressText != nil {
		sets.Add("addressText", *in.AddressText)
	}

	if in.NIK != nil {
		kyc := h.KYC.Resolve(*in.NIK, str(in.KTPName), str(in.KYCAttestation))
		if !kyc.Verified {
			httpx.Error(w, http.StatusUnprocessableEntity, "Verifikasi KTP gagal. Foto ulang KTP dengan jelas lalu coba lagi.")
			return
		}
		current, err := store.GetUserByID(ctx, h.DB, user.ID)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if current != nil && current.NIK != nil && *current.NIK != *kyc.NIK {
			httpx.Error(w, http.StatusConflict, "Akun ini sudah terverifikasi dengan identitas lain.")
			return
		}
		if dup, err := h.identityTaken(r, *kyc.NIK, kyc.KTPName, "", user.ID); err != nil {
			httpx.Internal(w, r, err)
			return
		} else if dup {
			httpx.Error(w, http.StatusConflict, "Identitas sudah terdaftar.")
			return
		}
		sets.Add("nik", *kyc.NIK)
		sets.Add("ktpName", kyc.KTPName)
		sets.Add("isVerified", true)
	}

	if err := store.UpdateUser(ctx, h.DB, user.ID, sets); err != nil {
		if db.IsUniqueViolation(err) {
			httpx.Error(w, http.StatusConflict, "Identitas atau email sudah terdaftar.")
			return
		}
		httpx.Internal(w, r, err)
		return
	}
	if in.Lat != nil && in.Lng != nil {
		if err := store.SetUserLocation(ctx, h.DB, user.ID, *in.Lat, *in.Lng); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	me, err := store.GetMe(ctx, h.DB, user.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, me)
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const forgotGenericMessage = "Jika email terdaftar, tautan pemulihan kata sandi telah dikirim."

// POST /auth/forgot-password
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email *string }
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !h.Limiter.Allow(w, middleware.RuleForgotEmail, middleware.EmailKey(str(in.Email))) {
		return
	}
	v := &httpx.V{}
	if httpx.Required(v, "email", in.Email) {
		v.Email("email", in.Email)
	}
	if v.Fail(w) {
		return
	}
	ctx := r.Context()
	u, err := store.GetUserByEmail(ctx, h.DB, *in.Email)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Respons SAMA untuk email terdaftar / tidak — cegah enumerasi akun.
	if u == nil {
		httpx.Message(w, forgotGenericMessage)
		return
	}

	// Token mentah hanya dikirim via email; DB menyimpan hash SHA-256-nya.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	token := hex.EncodeToString(raw)
	if err := store.SetResetToken(ctx, h.DB, u.ID, hashResetToken(token), time.Now().Add(15*time.Minute)); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	resetURL := strings.TrimRight(h.FrontendURL, "/") + "/reset-password?token=" + url.QueryEscape(token)
	// Gagal kirim email tetap dibalas generik (beda status = bocor info akun mana yang terdaftar).
	if err := h.Mail.Send(u.Email, "Atur Ulang Kata Sandi Rongsok.in", resetEmailHTML(resetURL)); err != nil {
		slog.Error("gagal mengirim email reset password", "err", err)
	}
	httpx.Message(w, forgotGenericMessage)
}

func resetEmailHTML(resetURL string) string {
	u := html.EscapeString(resetURL)
	return `
      <div style="font-family: sans-serif; padding: 20px; max-width: 600px; margin: 0 auto; border: 1px solid #e2e8f0; border-radius: 12px; background-color: #ffffff;">
        <h2 style="color: #111827; font-size: 20px; font-weight: 800; margin-bottom: 12px;">Atur Ulang Kata Sandi Rongsok.in</h2>
        <p style="color: #4b5563; font-size: 14px; line-height: 1.6; margin-bottom: 24px;">Halo,</p>
        <p style="color: #4b5563; font-size: 14px; line-height: 1.6; margin-bottom: 24px;">Kami menerima permintaan untuk mengatur ulang kata sandi akun Rongsok.in Anda. Silakan klik tombol di bawah ini untuk melanjutkan:</p>
        <div style="text-align: center; margin: 32px 0;">
          <a href="` + u + `" style="background-color: #10B981; color: white; padding: 12px 28px; text-decoration: none; border-radius: 8px; font-size: 14px; font-weight: bold; display: inline-block;">Atur Ulang Kata Sandi</a>
        </div>
        <p style="color: #6b7280; font-size: 12px; line-height: 1.6; margin-top: 24px;">Tautan ini hanya berlaku selama 15 menit. Jika Anda tidak merasa melakukan permintaan ini, silakan abaikan email ini secara aman.</p>
        <hr style="border: none; border-top: 1px solid #e5e7eb; margin: 24px 0;" />
        <p style="font-size: 11px; color: #9ca3af;">Tim Rongsok.in Marketplace</p>
      </div>`
}

// POST /auth/reset-password
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password *string }
	if !httpx.Decode(w, r, &in) {
		return
	}
	v := &httpx.V{}
	httpx.Required(v, "token", in.Token)
	validatePassword(v, in.Password)
	if v.Fail(w) {
		return
	}
	hash, err := security.HashPassword(*in.Password)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	ok, err := store.ConsumeResetToken(r.Context(), h.DB, hashResetToken(*in.Token), hash)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "Tautan tidak valid atau telah kedaluwarsa.")
		return
	}
	httpx.Message(w, "Kata sandi berhasil diperbarui.")
}
