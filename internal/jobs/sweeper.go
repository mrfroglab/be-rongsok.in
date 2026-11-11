// Package jobs: tugas latar (auto-batal order basi).
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/realtime"
	"github.com/mrfroglab/be-rongsok.in/internal/store"
)

const (
	// Order ON_THE_WAY > 3 jam tanpa ditandai sampai → dibatalkan.
	OnTheWayTimeout = 3 * time.Hour
	// Tawaran WAR berlaku 15 menit (sama dengan countdown OFFER_TTL_MS di FE).
	// Order forward (collectorId terisi) TIDAK kena — customer sengaja menunggu
	// satu lapak tertentu.
	WarOfferTimeout = 15 * time.Minute
	SweepInterval   = time.Minute
)

type Sweeper struct {
	DB  *pgxpool.Pool
	Hub *realtime.Hub
}

// Run menyapu tiap SweepInterval sampai ctx selesai.
func (s *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep menjalankan satu putaran.
func (s *Sweeper) Sweep(ctx context.Context) {
	now := time.Now()

	ids, err := store.CancelStaleOnTheWay(ctx, s.DB, now.Add(-OnTheWayTimeout))
	if err != nil {
		slog.Error("gagal menyapu order ON_THE_WAY basi", "err", err)
	}
	for _, id := range ids {
		s.Hub.Emit("order:"+id, "order_status_update", map[string]any{"orderId": id, "status": store.StatusCancelled})
		slog.Info("auto-batal: >3 jam dalam perjalanan tanpa 'sudah sampai'", "order", id)
	}

	ids, err = store.CancelExpiredWar(ctx, s.DB, now.Add(-WarOfferTimeout))
	if err != nil {
		slog.Error("gagal menyapu order war basi", "err", err)
	}
	for _, id := range ids {
		s.Hub.Emit("order:"+id, "order_status_update", map[string]any{"orderId": id, "status": store.StatusCancelled})
		// Bersihkan antrean SEMUA pengepul yang sempat dinotifikasi.
		collectors, err := store.OfferRecipients(ctx, s.DB, id)
		if err != nil {
			slog.Error("gagal membaca penerima tawaran", "order", id, "err", err)
			continue
		}
		for _, c := range collectors {
			s.Hub.Emit("collector:"+c, "order_taken", map[string]any{"orderId": id})
		}
		slog.Info("auto-batal: 15 menit war tanpa pengepul yang menerima", "order", id)
	}
}
