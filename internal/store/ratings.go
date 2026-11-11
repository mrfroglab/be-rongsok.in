package store

import (
	"context"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

const ratingCols = `id, "orderId", "raterId", "rateeId", score, "reviewText", "photoUrl", "createdAt"`

func (r *Rating) targets() []any {
	return []any{&r.ID, &r.OrderID, &r.RaterID, &r.RateeID, &r.Score, &r.ReviewText, &r.PhotoURL, &r.CreatedAt}
}

type NewRating struct {
	OrderID, RaterID, RateeID string
	Score                     int
	ReviewText, PhotoURL      *string
}

// CreateRating menyimpan rating lalu memperbarui avgRating penerima (panggil dalam transaksi).
func CreateRating(ctx context.Context, q db.DBTX, n NewRating) (*Rating, error) {
	var r Rating
	if err := q.QueryRow(ctx, `INSERT INTO "Rating" (id, "orderId", "raterId", "rateeId", score, "reviewText", "photoUrl", "createdAt")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+ratingCols,
		NewID(), n.OrderID, n.RaterID, n.RateeID, n.Score, n.ReviewText, n.PhotoURL, Now()).Scan(r.targets()...); err != nil {
		return nil, err
	}
	_, err := q.Exec(ctx, `UPDATE "User" SET "avgRating" = COALESCE(
		(SELECT avg(score)::float8 FROM "Rating" WHERE "rateeId" = $1), 0) WHERE id = $1`, n.RateeID)
	return &r, err
}

// RatingsForUser: rating yang diterima user (terbaru dulu) beserta info pemberi.
func RatingsForUser(ctx context.Context, q db.DBTX, rateeID string) ([]Rating, error) {
	rows, err := q.Query(ctx, `
		SELECT r.id, r."orderId", r."raterId", r."rateeId", r.score, r."reviewText", r."photoUrl", r."createdAt",
		       u.id, u.name, u."avatarUrl"
		FROM "Rating" r JOIN "User" u ON u.id = r."raterId"
		WHERE r."rateeId" = $1 ORDER BY r."createdAt" DESC`, rateeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rating{}
	for rows.Next() {
		var r Rating
		var rater RaterInfo
		if err := rows.Scan(append(r.targets(), &rater.ID, &rater.Name, &rater.AvatarURL)...); err != nil {
			return nil, err
		}
		r.Rater = &rater
		out = append(out, r)
	}
	return out, rows.Err()
}
