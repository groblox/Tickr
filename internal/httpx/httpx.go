// Package httpx holds small HTTP helpers shared by connectors and modules.
package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// UserAgent identifies Breaklist to public APIs that ask for one.
const UserAgent = "Breaklist/2.0 (+https://github.com/groblox/breaklist-lp)"

// Client is the shared HTTP client with a sane timeout.
var Client = &http.Client{Timeout: 20 * time.Second}

// Do performs a request with the shared client, adding a User-Agent.
func Do(ctx context.Context, method, url string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return Client.Do(req)
}

// GetJSON fetches url and decodes the JSON body into out.
func GetJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	return doJSON(ctx, http.MethodGet, url, nil, headers, out)
}

// PostJSON posts a JSON body and decodes the JSON response into out.
func PostJSON(ctx context.Context, url string, payload any, headers map[string]string, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	return doJSON(ctx, http.MethodPost, url, strings.NewReader(string(b)), h, out)
}

func doJSON(ctx context.Context, method, url string, body io.Reader, headers map[string]string, out any) error {
	if headers == nil {
		headers = map[string]string{}
	}
	if _, ok := headers["Accept"]; !ok {
		headers["Accept"] = "application/json"
	}
	resp, err := Do(ctx, method, url, body, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: status %d: %s", method, redact(url), resp.StatusCode, truncate(string(raw), 300))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding %s: %w", redact(url), err)
	}
	return nil
}

// GetBytes downloads a URL and returns the body (capped at 16MB).
func GetBytes(ctx context.Context, url string, headers map[string]string) ([]byte, string, error) {
	resp, err := Do(ctx, http.MethodGet, url, nil, headers)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("GET %s: status %d", redact(url), resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return raw, resp.Header.Get("Content-Type"), err
}

// GetBytesHeaders is GetBytes with full control over request headers
// (including User-Agent, which Do would otherwise set).
func GetBytesHeaders(ctx context.Context, url string, headers map[string]string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("GET %s: status %d", redact(url), resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return raw, resp.Header.Get("Content-Type"), err
}

// redact strips query strings so API keys never land in logs.
func redact(url string) string {
	if i := strings.Index(url, "?"); i >= 0 {
		return url[:i] + "?…"
	}
	return url
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
