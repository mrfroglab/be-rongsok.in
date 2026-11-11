package handler

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/mrfroglab/be-rongsok.in/internal/cloudinary"
	"github.com/mrfroglab/be-rongsok.in/internal/httpx"
)

const maxUploadBytes = 5 << 20 // 5 MB

// Whitelist eksplisit (bukan sekadar image/*) — tolak SVG (bisa berisi script).
var allowedImageTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/heic": true, "image/heif": true,
}

func uploadError(w http.ResponseWriter, msg string) {
	httpx.ErrorWithDetails(w, http.StatusBadRequest, msg, []any{})
}

// POST /upload — multipart, satu file di field "image" (wajib login).
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	data, filename, ok := readImagePart(w, r)
	if !ok {
		return
	}
	if data == nil {
		httpx.Error(w, http.StatusBadRequest, "No file uploaded. Please upload a valid image file.")
		return
	}
	res, err := h.Cloudinary.Upload(r.Context(), data, filename, "rongsokin")
	if errors.Is(err, cloudinary.ErrNotConfigured) {
		httpx.Error(w, http.StatusInternalServerError, "Server storage configuration error. Cloudinary is not configured.")
		return
	}
	var upErr *cloudinary.UploadError
	if errors.As(err, &upErr) && upErr.Status >= 400 && upErr.Status < 500 {
		slog.Warn("cloudinary menolak upload", "err", err)
		uploadError(w, "Gambar tidak valid atau formatnya tidak didukung.")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.OK(w, http.StatusOK, map[string]any{
		"url": res.SecureURL, "publicId": res.PublicID, "bytes": res.Bytes, "format": res.Format,
	})
}

// readImagePart membaca part "image" secara streaming. data nil = tak ada file.
// ok=false = respons error sudah ditulis.
func readImagePart(w http.ResponseWriter, r *http.Request) (data []byte, filename string, ok bool) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "multipart/form-data" {
		return nil, "", true
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		uploadError(w, "Form upload tidak valid.")
		return nil, "", false
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, "", true
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				uploadError(w, "File size too large. Maximum size allowed is 5MB.")
			} else {
				uploadError(w, "Form upload tidak valid.")
			}
			return nil, "", false
		}
		if part.FormName() != "image" || part.FileName() == "" {
			part.Close()
			continue
		}
		mt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if !allowedImageTypes[mt] {
			uploadError(w, "Invalid file type. Only images are allowed!")
			return nil, "", false
		}
		data, err := io.ReadAll(io.LimitReader(part, maxUploadBytes+1))
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				uploadError(w, "File size too large. Maximum size allowed is 5MB.")
			} else {
				uploadError(w, "Form upload tidak valid.")
			}
			return nil, "", false
		}
		if len(data) > maxUploadBytes {
			uploadError(w, "File size too large. Maximum size allowed is 5MB.")
			return nil, "", false
		}
		return data, part.FileName(), true
	}
}
