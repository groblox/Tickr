package connectors

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"tickr/internal/config"
	"tickr/internal/httpx"
)

// DropboxRedirectURI must match the URI registered on the Dropbox app.
const DropboxRedirectURI = "http://localhost:3030/auth/dropbox/callback"

// DropboxAuthURL builds the PKCE consent URL and returns it with the verifier
// that must be presented when exchanging the code.
func DropboxAuthURL(appKey string) (authURL, verifier string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authURL = fmt.Sprintf("https://www.dropbox.com/oauth2/authorize?client_id=%s&response_type=code&code_challenge_method=S256&code_challenge=%s&redirect_uri=%s&token_access_type=offline",
		url.QueryEscape(appKey), url.QueryEscape(challenge), url.QueryEscape(DropboxRedirectURI))
	return authURL, verifier, nil
}

// DropboxExchangeCode swaps an authorization code for a refresh token.
func DropboxExchangeCode(ctx context.Context, appKey, code, verifier string) (string, error) {
	val := url.Values{}
	val.Set("grant_type", "authorization_code")
	val.Set("code", code)
	val.Set("client_id", appKey)
	val.Set("code_verifier", verifier)
	val.Set("redirect_uri", DropboxRedirectURI)
	var tok struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := postForm(ctx, "https://api.dropboxapi.com/oauth2/token", val, &tok); err != nil {
		return "", err
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("dropbox returned no refresh token")
	}
	return tok.RefreshToken, nil
}

// Dropbox is a minimal client for reading the task file.
type Dropbox struct {
	cfg         config.Dropbox
	accessToken string
}

// NewDropbox creates a client from config.
func NewDropbox(cfg config.Dropbox) *Dropbox { return &Dropbox{cfg: cfg} }

func (d *Dropbox) token(ctx context.Context) (string, error) {
	if d.accessToken != "" {
		return d.accessToken, nil
	}
	if d.cfg.RefreshToken == "" {
		return "", fmt.Errorf("Dropbox is not linked yet")
	}
	val := url.Values{}
	val.Set("grant_type", "refresh_token")
	val.Set("refresh_token", d.cfg.RefreshToken)
	val.Set("client_id", d.cfg.AppKey)
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := postForm(ctx, "https://api.dropboxapi.com/oauth2/token", val, &tok); err != nil {
		return "", fmt.Errorf("refreshing dropbox token: %w", err)
	}
	d.accessToken = tok.AccessToken
	return tok.AccessToken, nil
}

func (d *Dropbox) rpc(ctx context.Context, endpoint string, arg any, extraHeaders map[string]string) ([]byte, int, error) {
	token, err := d.token(ctx)
	if err != nil {
		return nil, 0, err
	}
	var body io.Reader
	headers := map[string]string{"Authorization": "Bearer " + token}
	if arg != nil {
		b, _ := json.Marshal(arg)
		body = bytes.NewReader(b)
		headers["Content-Type"] = "application/json"
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	resp, err := httpx.Do(ctx, http.MethodPost, endpoint, body, headers)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return raw, resp.StatusCode, nil
}

// ResolvePath returns the configured file path or the first .json in the app folder.
func (d *Dropbox) ResolvePath(ctx context.Context) (string, error) {
	if d.cfg.FilePath != "" {
		return d.cfg.FilePath, nil
	}
	raw, status, err := d.rpc(ctx, "https://api.dropboxapi.com/2/files/list_folder", map[string]string{"path": ""}, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("dropbox list_folder: status %d: %s", status, raw)
	}
	var res struct {
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path_lower"`
			Tag  string `json:".tag"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	for _, e := range res.Entries {
		if e.Tag == "file" && strings.HasSuffix(strings.ToLower(e.Name), ".json") {
			return e.Path, nil
		}
	}
	return "", fmt.Errorf("no .json file found in the Dropbox app folder")
}

// GetTasks downloads and parses the task file. A missing file yields no tasks.
func (d *Dropbox) GetTasks(ctx context.Context) ([]string, error) {
	path, err := d.ResolvePath(ctx)
	if err != nil {
		return nil, err
	}
	arg, _ := json.Marshal(map[string]string{"path": path})
	raw, status, err := d.rpc(ctx, "https://content.dropboxapi.com/2/files/download", nil, map[string]string{"Dropbox-API-Arg": string(arg)})
	if err != nil {
		return nil, err
	}
	if status == http.StatusConflict || status == http.StatusNotFound {
		return []string{}, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("dropbox download: status %d: %s", status, raw)
	}
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		return ParseTasksJSON(raw)
	}
	return ParseTaskLines(string(raw)), nil
}

// ParseTaskLines reads a plain-text list, skipping blanks and # comments.
func ParseTaskLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// ParseTasksJSON accepts a JSON array of strings or {text, done} objects, or
// an object holding such an array under tasks/items/data/list.
func ParseTasksJSON(data []byte) ([]string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return []string{}, nil
	}
	var arr []any
	if err := json.Unmarshal(data, &arr); err == nil {
		return tasksFromSlice(arr), nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		for _, key := range []string{"tasks", "items", "data", "list"} {
			if s, ok := obj[key].([]any); ok {
				return tasksFromSlice(s), nil
			}
		}
		for _, v := range obj {
			if s, ok := v.([]any); ok {
				return tasksFromSlice(s), nil
			}
		}
	}
	return nil, fmt.Errorf("task file is not a JSON list")
}

func tasksFromSlice(arr []any) []string {
	var out []string
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				out = append(out, s)
			}
		case map[string]any:
			if done, _ := v["done"].(bool); done {
				continue
			}
			for _, key := range []string{"text", "title", "name", "task"} {
				if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
					break
				}
			}
		}
	}
	return out
}
