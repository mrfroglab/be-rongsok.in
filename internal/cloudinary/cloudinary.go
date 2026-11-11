// Package cloudinary: upload gambar (signed upload lewat REST API Cloudinary).
package cloudinary

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // skema tanda tangan Cloudinary memang SHA-1
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured: env CLOUDINARY_* belum lengkap.
var ErrNotConfigured = errors.New("cloudinary environment variables are not configured")

type Client struct {
	CloudName, APIKey, APISecret string
	HTTP                         *http.Client
	// BaseURL bisa diganti untuk tes.
	BaseURL string
}

func (c *Client) Configured() bool { return c.CloudName != "" && c.APIKey != "" && c.APISecret != "" }

type Result struct {
	SecureURL string `json:"secure_url"`
	PublicID  string `json:"public_id"`
	Bytes     int64  `json:"bytes"`
	Format    string `json:"format"`
}

// AllowedFormats: Cloudinary memeriksa isi file (bukan cuma mimetype dari client).
const AllowedFormats = "jpg,jpeg,png,webp,heic,heif"

// Upload mengunggah satu gambar ke folder.
func (c *Client) Upload(ctx context.Context, data []byte, filename, folder string) (*Result, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	params := map[string]string{
		"allowed_formats": AllowedFormats,
		"folder":          folder,
		"timestamp":       strconv.FormatInt(time.Now().Unix(), 10),
	}
	params["signature"] = c.sign(params)
	params["api_key"] = c.APIKey

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range params {
		if err := mw.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(data); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudinary.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/v1_1/%s/image/upload", base, c.CloudName), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return nil, &UploadError{Status: resp.StatusCode, Message: e.Error.Message}
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// UploadError: Cloudinary menolak upload (mis. format bukan gambar).
type UploadError struct {
	Status  int
	Message string
}

func (e *UploadError) Error() string { return fmt.Sprintf("cloudinary %d: %s", e.Status, e.Message) }

// sign: SHA-1 dari parameter terurut "k=v&k2=v2" + api_secret.
func (c *Client) sign(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + params[k]
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "&") + c.APISecret)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}
