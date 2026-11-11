package store

import (
	"context"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

// CanViewOrder: siapa yang boleh melihat sebuah order (REST detail & room order:<id>):
//   - admin
//   - customer pemilik order
//   - pengepul yang sudah ditetapkan (forward, atau pemenang war)
//   - pengepul kandidat war (order PENDING tanpa collector) yang dinotifikasi & belum menolak
func CanViewOrder(ctx context.Context, q db.DBTX, user *AuthUser, o *Order) (bool, error) {
	if user == nil || o == nil {
		return false, nil
	}
	if user.Role == RoleAdmin || IsOrderParty(user.ID, o) {
		return true, nil
	}
	if o.Status == StatusPending && o.CollectorID == nil {
		return IsOfferRecipient(ctx, q, o.ID, user.ID)
	}
	return false, nil
}

// IsOrderParty: customer atau pengepul yang ditetapkan pada order.
func IsOrderParty(userID string, o *Order) bool {
	return o != nil && (o.CustomerID == userID || (o.CollectorID != nil && *o.CollectorID == userID))
}
