package httpx

import (
	"math"
	"strconv"
)

// ParseFinite mengurai string angka desimal; false bila bukan angka finite.
func ParseFinite(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// Number meniru Number(x) di JS untuk angka JSON & string angka; false bila tidak finite.
func Number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case string:
		return ParseFinite(x)
	}
	return 0, false
}
