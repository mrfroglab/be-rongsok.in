package cloudinary

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Contoh resmi dari dokumentasi Cloudinary ("Generating authentication signatures").
func TestSignMatchesCloudinaryDocs(t *testing.T) {
	c := &Client{APISecret: "abcd"}
	got := c.sign(map[string]string{
		"eager":     "w_400,h_300,c_pad|w_260,h_200,c_crop",
		"public_id": "sample_image",
		"timestamp": "1315060510",
	})
	if got != "bfd09f95f331f558cbd1320e67aa8d488770583e" {
		t.Fatalf("signature = %s", got)
	}
}

func TestUpload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1_1/demo/image/upload" {
			t.Errorf("path %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		f := r.MultipartForm.Value
		c := &Client{APISecret: "sec"}
		want := c.sign(map[string]string{"allowed_formats": f["allowed_formats"][0], "folder": f["folder"][0], "timestamp": f["timestamp"][0]})
		if f["signature"][0] != want || f["api_key"][0] != "key" || f["folder"][0] != "rongsokin" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "bad signature"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"secure_url": "https://res/x.png", "public_id": "rongsokin/x", "bytes": 1, "format": "png"})
	}))
	defer srv.Close()

	c := &Client{CloudName: "demo", APIKey: "key", APISecret: "sec", BaseURL: srv.URL}
	res, err := c.Upload(context.Background(), []byte("x"), "x.png", "rongsokin")
	if err != nil || res.SecureURL != "https://res/x.png" || res.PublicID != "rongsokin/x" {
		t.Fatalf("upload: %+v %v", res, err)
	}
	c.APISecret = "salah"
	_, err = c.Upload(context.Background(), []byte("x"), "x.png", "rongsokin")
	var ue *UploadError
	if !errors.As(err, &ue) || ue.Status != 401 {
		t.Fatalf("error Cloudinary harus jadi UploadError: %v", err)
	}
	if _, err := (&Client{}).Upload(context.Background(), nil, "", ""); !errors.Is(err, ErrNotConfigured) {
		t.Error("tanpa konfigurasi → ErrNotConfigured")
	}
}
