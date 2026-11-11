package httpx

import (
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// V mengumpulkan issue validasi. Pakai: v := &httpx.V{}; v.X(...); if v.Fail(w) { return }.
type V struct{ issues []Issue }

// Add mencatat issue untuk field path.
func (v *V) Add(path, msg string) {
	p := []any{}
	if path != "" {
		p = append(p, path)
	}
	v.issues = append(v.issues, Issue{Code: "custom", Path: p, Message: msg})
}

// Fail menulis 422 bila ada issue. true = respons sudah ditulis.
func (v *V) Fail(w http.ResponseWriter) bool {
	if len(v.issues) == 0 {
		return false
	}
	ValidationFailed(w, v.issues)
	return true
}

// Required: field wajib ada (pointer non-nil).
func Required[T any](v *V, path string, p *T) bool {
	if p == nil {
		v.Add(path, "Required")
		return false
	}
	return true
}

// StrLen cek panjang string (dalam karakter). min/max <0 = tanpa batas.
// s nil (tidak dikirim) dilewati — gabungkan dengan Required bila wajib.
func (v *V) StrLen(path string, s *string, min, max int) {
	if s == nil {
		return
	}
	n := utf8.RuneCountInString(*s)
	if min >= 0 && n < min {
		v.Add(path, "String must contain at least "+itoa(min)+" character(s)")
	}
	if max >= 0 && n > max {
		v.Add(path, "String must contain at most "+itoa(max)+" character(s)")
	}
}

// Range cek angka finite dalam [min, max].
func (v *V) Range(path string, f *float64, min, max float64) {
	if f == nil {
		return
	}
	if math.IsNaN(*f) || math.IsInf(*f, 0) || *f < min || *f > max {
		v.Add(path, "Number must be between "+ftoa(min)+" and "+ftoa(max))
	}
}

// Positive cek angka > 0 dan <= max.
func (v *V) Positive(path string, f *float64, max float64) {
	if f == nil {
		return
	}
	if !(*f > 0) || *f > max {
		v.Add(path, "Number must be greater than 0 and at most "+ftoa(max))
	}
}

// OneOf cek nilai enum.
func (v *V) OneOf(path string, s *string, allowed ...string) {
	if s == nil {
		return
	}
	for _, a := range allowed {
		if *s == a {
			return
		}
	}
	v.Add(path, "Invalid enum value. Expected '"+strings.Join(allowed, "' | '")+"'")
}

// Email: aturan sama dengan z.string().email().
func (v *V) Email(path string, s *string) {
	if s == nil {
		return
	}
	if !IsEmail(*s) {
		v.Add(path, "Invalid email")
	}
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9_'+\-.]*[A-Za-z0-9_+\-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`)

// IsEmail mereplikasi regex email zod (tanpa lookahead: tak diawali titik & tak ada "..").
func IsEmail(s string) bool {
	if strings.HasPrefix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	return emailRe.MatchString(s)
}

func itoa(n int) string { return strconv.Itoa(n) }

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
