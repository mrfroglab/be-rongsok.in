package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

// AuthUser = identitas minimal untuk otorisasi (role selalu dari DB, bukan JWT).
type AuthUser struct {
	ID   string
	Role string
}

// GetAuthUser mengembalikan nil bila user tidak ada.
func GetAuthUser(ctx context.Context, q db.DBTX, id string) (*AuthUser, error) {
	var u AuthUser
	err := q.QueryRow(ctx, `SELECT id, role::text FROM "User" WHERE id = $1`, id).Scan(&u.ID, &u.Role)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UserRecord = baris User untuk login/registrasi.
type UserRecord struct {
	ID           string
	Name         string
	Email        string
	PasswordHash string
	Phone        *string
	Role         string
	AvatarURL    *string
	IsVerified   bool
	NIK          *string
}

const userRecordCols = `id, name, email, "passwordHash", phone, role::text, "avatarUrl", "isVerified", nik`

func scanUserRecord(row pgx.Row) (*UserRecord, error) {
	var u UserRecord
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Phone, &u.Role, &u.AvatarURL, &u.IsVerified, &u.NIK)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByEmail: nil bila tidak ada (pencocokan persis, seperti Prisma findUnique).
func GetUserByEmail(ctx context.Context, q db.DBTX, email string) (*UserRecord, error) {
	return scanUserRecord(q.QueryRow(ctx, `SELECT `+userRecordCols+` FROM "User" WHERE email = $1`, email))
}

func GetUserByID(ctx context.Context, q db.DBTX, id string) (*UserRecord, error) {
	return scanUserRecord(q.QueryRow(ctx, `SELECT `+userRecordCols+` FROM "User" WHERE id = $1`, id))
}

// NIKTaken: ada user lain (selain excludeID) dengan NIK ini?
func NIKTaken(ctx context.Context, q db.DBTX, nik, excludeID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "User" WHERE nik = $1 AND id <> $2)`, nik, excludeID).Scan(&exists)
	return exists, err
}

// KTPNameTaken: ada akun ber-NIK lain dengan nama KTP sama (case-insensitive)?
func KTPNameTaken(ctx context.Context, q db.DBTX, name, excludeID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM "User"
		WHERE nik IS NOT NULL AND lower("ktpName") = lower($1) AND id <> $2)`, name, excludeID).Scan(&exists)
	return exists, err
}

type NewUser struct {
	Name         string
	Email        string
	PasswordHash string
	Role         string
	Phone        *string
	AvatarURL    *string
	AddressText  *string
	NIK          *string
	KTPName      *string
	IsVerified   bool
}

func CreateUser(ctx context.Context, q db.DBTX, n NewUser) (*UserRecord, error) {
	return scanUserRecord(q.QueryRow(ctx, `
		INSERT INTO "User" (id, name, email, "passwordHash", role, phone, "avatarUrl", "addressText",
		                    nik, "ktpName", "isVerified", "createdAt")
		VALUES ($1, $2, $3, $4, $5::"Role", $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+userRecordCols,
		NewID(), n.Name, n.Email, n.PasswordHash, n.Role, n.Phone, n.AvatarURL, n.AddressText,
		n.NIK, n.KTPName, n.IsVerified, Now()))
}

// GetMe: profil lengkap user sendiri + koordinat. nil bila tidak ada.
func GetMe(ctx context.Context, q db.DBTX, id string) (*Me, error) {
	var m Me
	err := q.QueryRow(ctx, `
		SELECT id, name, email, role::text, "avgRating", "avatarUrl", phone, "createdAt",
		       "isVerified", nik, "ktpName", "addressText",
		       ST_Y(location::geometry), ST_X(location::geometry)
		FROM "User" WHERE id = $1`, id).Scan(
		&m.ID, &m.Name, &m.Email, &m.Role, &m.AvgRating, &m.AvatarURL, &m.Phone, &m.CreatedAt,
		&m.IsVerified, &m.NIK, &m.KTPName, &m.AddressText, &m.Lat, &m.Lng)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateUser menerapkan SET parsial.
func UpdateUser(ctx context.Context, q db.DBTX, id string, s *Sets) error {
	if s.Empty() {
		return nil
	}
	args := append(s.Args, id)
	_, err := q.Exec(ctx, `UPDATE "User" SET `+s.String()+` WHERE id = $`+itoa(len(args)), args...)
	return err
}

func SetUserLocation(ctx context.Context, q db.DBTX, id string, lat, lng float64) error {
	_, err := q.Exec(ctx, `UPDATE "User"
		SET location = ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography WHERE id = $3`, lng, lat, id)
	return err
}

// GetUserCoords: nil bila user tak punya lokasi.
func GetUserCoords(ctx context.Context, q db.DBTX, id string) (*LatLng, error) {
	var lat, lng *float64
	err := q.QueryRow(ctx, `SELECT ST_Y(location::geometry), ST_X(location::geometry)
		FROM "User" WHERE id = $1`, id).Scan(&lat, &lng)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return latLng(lat, lng), nil
}

func SetResetToken(ctx context.Context, q db.DBTX, id, tokenHash string, expires time.Time) error {
	_, err := q.Exec(ctx, `UPDATE "User" SET "resetPasswordToken" = $1, "resetPasswordExpires" = $2 WHERE id = $3`,
		tokenHash, expires.UTC(), id)
	return err
}

// ConsumeResetToken mengganti password bila token (hash) cocok & belum kedaluwarsa.
// Atomik: token langsung dihapus sehingga tidak bisa dipakai dua kali.
func ConsumeResetToken(ctx context.Context, q db.DBTX, tokenHash, newPasswordHash string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE "User"
		SET "passwordHash" = $1, "resetPasswordToken" = NULL, "resetPasswordExpires" = NULL
		WHERE id = (SELECT id FROM "User" WHERE "resetPasswordToken" = $2 AND "resetPasswordExpires" >= $3 LIMIT 1)`,
		newPasswordHash, tokenHash, Now())
	return tag.RowsAffected() == 1, err
}

func CountUsers(ctx context.Context, q db.DBTX) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM "User"`).Scan(&n)
	return n, err
}
