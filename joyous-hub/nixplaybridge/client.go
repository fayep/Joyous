// Package nixplaybridge implements the reverse-engineered Nixplay mobile app
// REST API (https://mobile-api.nixplay.com) used to upload photos into a
// Nixplay playlist ("gallery") so they sync out to assigned frames.
//
// Reverse-engineered by decompiling the Nixplay Android app's Hermes JS
// bundle — see Nixplay/docs/mobile_api.md in the repo root for the full
// writeup and caveats. This has NOT been confirmed against live traffic;
// treat the exact request/response shapes as best-effort until validated
// against a real account.
package nixplaybridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://mobile-api.nixplay.com"

// mobile-api.nixplay.com sits behind a WAF that 403s default HTTP client User-Agents
// (curl, Go-http-client) but passes anything that looks like a browser or the app's
// own OkHttp client. The app itself uses OkHttp (see decompiled RetrofitClient.java).
const nixplayUserAgent = "okhttp/4.9.3"

// Client is a signed-in Nixplay mobile API session.
type Client struct {
	http     *http.Client
	email    string
	password string

	mu          sync.Mutex // protects token/tokenExpiry
	token       string
	tokenExpiry time.Time

	// signInMu serializes ensureToken's check-then-sign-in so concurrent
	// callers (e.g. the periodic playlist refresh and a concurrent
	// send.image) don't each independently discover an expired token and
	// both perform a redundant /v1/auth/signin.
	signInMu sync.Mutex
}

// NewClient builds a client for the given Nixplay account. Call EnsureToken
// (or any request method, which calls it internally) to sign in.
func NewClient(email, password string) *Client {
	return &Client{
		http:     &http.Client{Timeout: 60 * time.Second},
		email:    email,
		password: password,
	}
}

// Playlist is a Nixplay "gallery" — a named photo collection assignable to frames.
type Playlist struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PictureCount int    `json:"picture_count"`
}

type signInResponse struct {
	Token string `json:"token"`
}

// SignIn authenticates and stores the session JWT.
func (c *Client) SignIn(ctx context.Context) error {
	body := map[string]string{
		"username":        c.email,
		"password":        c.password,
		"deviceId":        "joyous-hub-nixplay-bridge",
		"notificationKey": "",
		"platform":        "android",
		"model":           "joyous-hub",
		"version":         "3.73.2",
		"env":             "prod",
	}
	var resp signInResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/auth/signin", nil, body, false, &resp); err != nil {
		return fmt.Errorf("nixplay signin: %w", err)
	}
	if resp.Token == "" {
		return fmt.Errorf("nixplay signin: empty token in response")
	}
	c.mu.Lock()
	c.token = resp.Token
	c.tokenExpiry = jwtExpiry(resp.Token)
	c.mu.Unlock()
	return nil
}

// ensureToken signs in if there's no token yet or it's near expiry.
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.signInMu.Lock()
	defer c.signInMu.Unlock()

	c.mu.Lock()
	token := c.token
	expiry := c.tokenExpiry
	c.mu.Unlock()
	if token != "" && (expiry.IsZero() || time.Until(expiry) > time.Minute) {
		return token, nil
	}
	if err := c.SignIn(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token, nil
}

// jwtExpiry pulls the "exp" claim out of a JWT without verifying its signature
// (we trust it because we just received it directly from Nixplay over TLS).
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

// ListPlaylists returns the account's Nixplay galleries. Confirmed live: a
// bare JSON array of playlist objects (see Nixplay/docs/mobile_api.md) —
// decoded loosely here in case an account variant wraps it or uses a
// slightly different key name.
func (c *Client) ListPlaylists(ctx context.Context) ([]Playlist, error) {
	if _, err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/v6/playlists/", nil, nil, true, &raw); err != nil {
		var wrapped struct {
			Playlists []map[string]any `json:"playlists"`
		}
		if err2 := c.doJSON(ctx, http.MethodGet, "/v6/playlists/", nil, nil, true, &wrapped); err2 != nil {
			return nil, fmt.Errorf("nixplay list playlists: %w", err)
		}
		raw = wrapped.Playlists
	}
	out := make([]Playlist, 0, len(raw))
	for _, m := range raw {
		p := Playlist{
			ID:           firstString(m, "id", "playlistId", "playlist_id"),
			Name:         firstString(m, "name", "title"),
			PictureCount: firstInt(m, "picture_count", "pictureCount"),
		}
		if p.ID == "" {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
	}
	return ""
}

func firstInt(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int(t)
			case string:
				if n, err := strconv.Atoi(t); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

// s3TokenResponse is the real /v1/photos/S3token shape (confirmed live against
// a real account — the fields and nesting here do NOT match what the
// decompiled JS suggested; see Nixplay/docs/mobile_api.md).
type s3TokenResponse struct {
	Data struct {
		Key            string `json:"key"`
		ACL            string `json:"acl"`
		AWSAccessKeyID string `json:"AWSAccessKeyId"`
		Policy         string `json:"Policy"`
		Signature      string `json:"Signature"`
		BatchUploadID  string `json:"batchUploadId"`
		FileType       string `json:"fileType"`
		S3UploadURL    string `json:"s3UploadUrl"`
	} `json:"data"`
}

// UploadPhoto uploads a single JPEG to the given playlist: it opens an
// upload batch, requests a presigned S3 POST policy, uploads directly to S3,
// then finalizes the batch so Nixplay associates the photo with the playlist
// and syncs it out to assigned frames. data must already be JPEG-encoded —
// this does not convert HEIC or other formats; the caller must do that first.
func (c *Client) UploadPhoto(ctx context.Context, playlistID, fileName string, data []byte) error {
	if _, err := c.ensureToken(ctx); err != nil {
		return err
	}

	const fileType = "image/jpeg"

	uploadToken, err := c.openUploadBatch(ctx, playlistID)
	if err != nil {
		return fmt.Errorf("open upload batch: %w", err)
	}

	s3, err := c.getS3Token(ctx, uploadToken, fileName, fileType, len(data))
	if err != nil {
		return fmt.Errorf("get S3 upload credentials: %w", err)
	}

	if err := postToS3(ctx, c.http, s3, fileType, fileName, data); err != nil {
		return fmt.Errorf("upload to S3: %w", err)
	}

	if err := c.completeUpload(ctx, uploadToken); err != nil {
		return fmt.Errorf("finalize upload: %w", err)
	}
	return nil
}

func (c *Client) openUploadBatch(ctx context.Context, playlistID string) (string, error) {
	playlistIDs := []int64{}
	if playlistID != "" {
		// The API wants numeric playlist ids in the playlistIds array — not the
		// "albumId" field, which is a different (album, not playlist) concept
		// and rejects our playlist ids with "Invalid parameter: albumId".
		id, err := strconv.ParseInt(playlistID, 10, 64)
		if err != nil {
			return "", fmt.Errorf("playlist id %q is not numeric: %w", playlistID, err)
		}
		playlistIDs = append(playlistIDs, id)
	}
	body := map[string]any{
		"playlistIds": playlistIDs,
		"friends":     []string{},
		"total":       1,
		"camera":      true,
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v2/photos/receivers", nil, body, true, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("empty upload token in response")
	}
	return resp.Token, nil
}

func (c *Client) getS3Token(ctx context.Context, uploadToken, fileName, fileType string, fileSize int) (s3TokenResponse, error) {
	params := url.Values{}
	params.Set("uploadToken", uploadToken)
	params.Set("fileName", fileName)
	params.Set("fileType", fileType)
	params.Set("fileSize", strconv.Itoa(fileSize))
	var resp s3TokenResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/photos/S3token", params, nil, true, &resp); err != nil {
		return s3TokenResponse{}, err
	}
	if resp.Data.S3UploadURL == "" || resp.Data.Key == "" {
		return s3TokenResponse{}, fmt.Errorf("incomplete S3 upload credentials in response")
	}
	return resp, nil
}

func (c *Client) completeUpload(ctx context.Context, uploadToken string) error {
	body := map[string]string{"token": uploadToken}
	return c.doJSON(ctx, http.MethodPost, "/v3/photos/upload-completed", nil, body, true, nil)
}

// postToS3 performs the S3 POST-policy upload. Field order matters for S3:
// "key" first, "file" last; the API response doesn't spell out Content-Type/
// success_action_status/x-amz-meta-batch-upload-id as form fields directly,
// but the signed Policy's conditions require them (confirmed live).
func postToS3(ctx context.Context, client *http.Client, s3 s3TokenResponse, fileType, fileName string, data []byte) error {
	d := s3.Data
	fields := [][2]string{
		{"key", d.Key},
		{"acl", d.ACL},
		{"AWSAccessKeyId", d.AWSAccessKeyID},
		{"Policy", d.Policy},
		{"Signature", d.Signature},
		{"Content-Type", fileType},
		{"success_action_status", "201"},
		{"x-amz-meta-batch-upload-id", d.BatchUploadID},
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, kv := range fields {
		if err := w.WriteField(kv[0], kv[1]); err != nil {
			return err
		}
	}
	fw, err := w.CreateFormFile("file", fileName)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.S3UploadURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("User-Agent", nixplayUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("S3 upload HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// doJSON performs an authenticated (or anonymous) JSON request against the
// Nixplay mobile API. out may be nil for requests with no meaningful body.
func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any, authed bool, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	u := baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", nixplayUserAgent)
	if authed {
		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}
