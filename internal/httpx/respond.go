// Package httpx: helper HTTP bersama — format respons, decoding & validasi body.
//
// Format respons dipertahankan identik dengan backend Express lama:
//
//	sukses: {"status":"success","data":...}
//	gagal:  {"status":"error","message":"...","errors":[...]?}
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type okBody struct {
	Status string `json:"status"`
	Data   any    `json:"data"`
}

type messageBody struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type errorsBody struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Errors  any    `json:"errors"`
}

// JSON menulis v sebagai JSON dengan status code (tanpa escape HTML: & < > apa adanya).
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("gagal menulis respons", "err", err)
	}
}

// OK: {"status":"success","data":data}.
func OK(w http.ResponseWriter, status int, data any) {
	JSON(w, status, okBody{Status: "success", Data: data})
}

// Message: {"status":"success","message":msg}.
func Message(w http.ResponseWriter, msg string) {
	JSON(w, http.StatusOK, messageBody{Status: "success", Message: msg})
}

// Error: {"status":"error","message":msg}.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, messageBody{Status: "error", Message: msg})
}

// ErrorWithDetails: {"status":"error","message":msg,"errors":errors}.
func ErrorWithDetails(w http.ResponseWriter, status int, msg string, errors any) {
	JSON(w, status, errorsBody{Status: "error", Message: msg, Errors: errors})
}

// Internal mencatat err dan membalas 500 generik (detail tidak bocor ke client).
func Internal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	ErrorWithDetails(w, http.StatusInternalServerError, "Internal Server Error", []any{})
}
