// Package security: JWT, hash password, dan attestation KYC.
package security

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// TokenTTL = masa berlaku access token (sama dengan versi Express: 24 jam).
const TokenTTL = 24 * time.Hour

// Claims kompatibel dengan token yang diterbitkan jsonwebtoken di versi Node
// ({ id, role, iat, exp }) — sesi user tidak putus saat migrasi.
type Claims struct {
	ID   string `json:"id"`
	Role string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

type Tokens struct{ secret []byte }

func NewTokens(secret string) *Tokens { return &Tokens{secret: []byte(secret)} }

// Issue menerbitkan token HS256 berisi id + role.
func (t *Tokens) Issue(userID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		ID:   userID,
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// Verify memvalidasi tanda tangan (hanya HS256) & masa berlaku; mengembalikan id user.
func (t *Tokens) Verify(token string) (string, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return "", err
	}
	if c.ID == "" {
		return "", errors.New("token tanpa id")
	}
	return c.ID, nil
}

// bcrypt cost 10 = sama dengan bcryptjs.genSalt(10); hash lama ($2a/$2b) tetap valid.
const bcryptCost = 10

// MaxPasswordBytes: bcrypt hanya memakai 72 byte pertama.
const MaxPasswordBytes = 72

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

func CheckPassword(pw, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
