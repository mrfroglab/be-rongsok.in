package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
)

// MaxJSONBody = batas body JSON (sama dengan express.json({ limit: '1mb' })).
const MaxJSONBody = 1 << 20

// Issue: satu masalah validasi (bentuknya mengikuti issue zod: code/path/message).
type Issue struct {
	Code    string `json:"code"`
	Path    []any  `json:"path"`
	Message string `json:"message"`
}

// ReadBody membaca body JSON mentah. Seperti express.json(): body non-JSON
// (Content-Type lain) atau kosong dianggap `{}`. Menulis respons dan
// mengembalikan ok=false bila body terlalu besar.
func ReadBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Body == nil || !(ct == "application/json" || strings.HasSuffix(ct, "+json")) {
		return []byte("{}"), true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			Error(w, http.StatusRequestEntityTooLarge, "request entity too large")
			return nil, false
		}
		Error(w, http.StatusBadRequest, "Gagal membaca body request")
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return []byte("{}"), true
	}
	return body, true
}

// Decode = ReadBody + json.Unmarshal ke dst. JSON rusak → 400; tipe field
// salah (mis. string untuk angka) → 422 "Validation failed" seperti zod.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, ok := ReadBody(w, r)
	if !ok {
		return false
	}
	return Unmarshal(w, body, dst)
}

// Unmarshal: lihat Decode.
func Unmarshal(w http.ResponseWriter, body []byte, dst any) bool {
	err := json.Unmarshal(body, dst)
	if err == nil {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		path := []any{}
		if typeErr.Field != "" {
			for _, p := range strings.Split(typeErr.Field, ".") {
				path = append(path, p)
			}
		}
		ValidationFailed(w, []Issue{{
			Code:    "invalid_type",
			Path:    path,
			Message: "Expected " + jsTypeName(typeErr.Type) + ", received " + typeErr.Value,
		}})
		return false
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		ErrorWithDetails(w, http.StatusBadRequest, "Body JSON tidak valid", []any{})
		return false
	}
	ValidationFailed(w, []Issue{{Code: "custom", Path: []any{}, Message: err.Error()}})
	return false
}

// ValidationFailed: 422 {"status":"error","message":"Validation failed","errors":[...]}.
func ValidationFailed(w http.ResponseWriter, issues []Issue) {
	ErrorWithDetails(w, http.StatusUnprocessableEntity, "Validation failed", issues)
}

// jsTypeName: nama tipe ala zod (number/string/boolean/array/object).
func jsTypeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Float32, reflect.Float64, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "object"
	}
}

// Opt membedakan field yang tidak dikirim, dikirim null, atau dikirim bernilai.
type Opt[T any] struct {
	Set  bool
	Null bool
	V    T
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.V)
}
