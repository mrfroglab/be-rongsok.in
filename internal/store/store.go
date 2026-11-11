package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NewID: id baru (UUID v4 string, sama dengan @default(uuid()) Prisma).
func NewID() string { return uuid.NewString() }

// IsNotFound: true bila err = tidak ada baris.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Sets membangun klausa SET dinamis untuk UPDATE parsial.
type Sets struct {
	cols []string
	Args []any
}

// Add menambahkan "col" = $n.
func (s *Sets) Add(col string, v any) {
	s.Args = append(s.Args, v)
	s.cols = append(s.cols, fmt.Sprintf(`%q = $%d`, col, len(s.Args)))
}

// AddRaw menambahkan "col" = <expr>, expr boleh memakai placeholder $n berikutnya
// (gunakan Next() untuk nomornya).
func (s *Sets) AddRaw(col, expr string) { s.cols = append(s.cols, fmt.Sprintf(`%q = %s`, col, expr)) }

// Next mendaftarkan argumen dan mengembalikan placeholder-nya ($n).
func (s *Sets) Next(v any) string {
	s.Args = append(s.Args, v)
	return fmt.Sprintf("$%d", len(s.Args))
}

func (s *Sets) Empty() bool { return len(s.cols) == 0 }

func (s *Sets) String() string { return strings.Join(s.cols, ", ") }

func itoa(n int) string { return strconv.Itoa(n) }

// latLng menggabungkan dua kolom nullable (ST_Y/ST_X) jadi *LatLng.
func latLng(lat, lng *float64) *LatLng {
	if lat == nil || lng == nil {
		return nil
	}
	return &LatLng{Lat: *lat, Lng: *lng}
}
