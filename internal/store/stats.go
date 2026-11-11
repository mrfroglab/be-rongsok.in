package store

import (
	"context"
	"time"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

type PublicStats struct {
	TotalTransactions int     `json:"totalTransactions"`
	TotalCollectors   int     `json:"totalCollectors"`
	TotalCustomers    int     `json:"totalCustomers"`
	TotalCategories   int     `json:"totalCategories"`
	TotalWeightKg     float64 `json:"totalWeightKg"`
}

// GetPublicStats: statistik landing page.
func GetPublicStats(ctx context.Context, q db.DBTX) (*PublicStats, error) {
	var s PublicStats
	err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM "Order" WHERE status = 'COMPLETED'),
		(SELECT count(*) FROM "CollectorProfile"),
		(SELECT count(*) FROM "User" WHERE role = 'CUSTOMER'),
		(SELECT count(*) FROM "WasteCategory"),
		(SELECT COALESCE(sum("actualWeight"), 0) FROM "Order" WHERE status = 'COMPLETED')`).Scan(
		&s.TotalTransactions, &s.TotalCollectors, &s.TotalCustomers, &s.TotalCategories, &s.TotalWeightKg)
	return &s, err
}

type LeaderboardEntry struct {
	Rank       int     `json:"rank"`
	UserID     string  `json:"userId"`
	Name       string  `json:"name"`
	AvatarURL  *string `json:"avatarUrl"`
	TotalKg    float64 `json:"totalKg"`
	OrderCount int     `json:"orderCount"`
}

// Leaderboard: top-N customer berdasarkan total berat order COMPLETED.
func Leaderboard(ctx context.Context, q db.DBTX, limit int) ([]LeaderboardEntry, error) {
	rows, err := q.Query(ctx, `
		SELECT g."customerId", COALESCE(u.name, 'Pengguna'), u."avatarUrl", g.total, g.cnt
		FROM (
			SELECT "customerId", sum("actualWeight") AS total, count(*)::int AS cnt
			FROM "Order" WHERE status = 'COMPLETED' AND "actualWeight" IS NOT NULL
			GROUP BY "customerId"
		) g LEFT JOIN "User" u ON u.id = g."customerId"
		ORDER BY g.total DESC, g."customerId" LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LeaderboardEntry{}
	for rows.Next() {
		var e LeaderboardEntry
		if err := rows.Scan(&e.UserID, &e.Name, &e.AvatarURL, &e.TotalKg, &e.OrderCount); err != nil {
			return nil, err
		}
		e.Rank = len(out) + 1
		out = append(out, e)
	}
	return out, rows.Err()
}

type MyRank struct {
	UserID     string  `json:"userId"`
	TotalKg    float64 `json:"totalKg"`
	OrderCount int     `json:"orderCount"`
	Rank       *int    `json:"rank"`
}

// LeaderboardRank: total & peringkat seorang customer (rank null bila 0 kg).
func LeaderboardRank(ctx context.Context, q db.DBTX, userID string) (*MyRank, error) {
	m := MyRank{UserID: userID}
	if err := q.QueryRow(ctx, `SELECT COALESCE(sum("actualWeight"), 0), count(*)::int FROM "Order"
		WHERE status = 'COMPLETED' AND "actualWeight" IS NOT NULL AND "customerId" = $1`, userID).Scan(&m.TotalKg, &m.OrderCount); err != nil {
		return nil, err
	}
	if m.TotalKg > 0 {
		var higher int
		if err := q.QueryRow(ctx, `SELECT count(*)::int FROM (
			SELECT "customerId" FROM "Order" WHERE status = 'COMPLETED' AND "actualWeight" IS NOT NULL
			GROUP BY "customerId" HAVING sum("actualWeight") > $1) h`, m.TotalKg).Scan(&higher); err != nil {
			return nil, err
		}
		rank := higher + 1
		m.Rank = &rank
	}
	return &m, nil
}

type AdminTotals struct {
	TotalWeightKg   float64
	TotalPayout     float64
	ActiveOrders    int
	TotalCustomers  int
	TotalCollectors int
}

// ActiveStatuses = order yang masih berjalan (dashboard admin).
var ActiveStatuses = []string{StatusPending, StatusConfirmed, StatusOnTheWay, StatusInProgress, StatusAwaitingConfirmation}

func GetAdminTotals(ctx context.Context, q db.DBTX) (*AdminTotals, error) {
	var t AdminTotals
	err := q.QueryRow(ctx, `SELECT
		(SELECT COALESCE(sum("actualWeight"), 0) FROM "Order" WHERE status = 'COMPLETED'),
		(SELECT COALESCE(sum("totalPrice"), 0) FROM "Order" WHERE status = 'COMPLETED'),
		(SELECT count(*) FROM "Order" WHERE status::text = ANY($1::text[])),
		(SELECT count(*) FROM "User" WHERE role = 'CUSTOMER'),
		(SELECT count(*) FROM "User" WHERE role = 'COLLECTOR')`, ActiveStatuses).Scan(
		&t.TotalWeightKg, &t.TotalPayout, &t.ActiveOrders, &t.TotalCustomers, &t.TotalCollectors)
	return &t, err
}

type DailyTotal struct {
	Count  int
	Weight float64
	Amount float64
}

// DailyCompleted: total order COMPLETED per tanggal (UTC) sejak since, key "YYYY-MM-DD".
func DailyCompleted(ctx context.Context, q db.DBTX, since time.Time) (map[string]DailyTotal, error) {
	rows, err := q.Query(ctx, `
		SELECT to_char(date("createdAt"), 'YYYY-MM-DD'), count(*)::int,
		       COALESCE(sum("actualWeight"), 0), COALESCE(sum("totalPrice"), 0)
		FROM "Order" WHERE status = 'COMPLETED' AND "createdAt" >= $1
		GROUP BY date("createdAt")`, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DailyTotal{}
	for rows.Next() {
		var key string
		var d DailyTotal
		if err := rows.Scan(&key, &d.Count, &d.Weight, &d.Amount); err != nil {
			return nil, err
		}
		out[key] = d
	}
	return out, rows.Err()
}
