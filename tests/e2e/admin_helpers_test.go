//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
)

const adminBase = "http://127.0.0.1:8080/api/v1"

// apiClient is a cookie-session client for the admin API.
type apiClient struct {
	t    *testing.T
	c    *http.Client
	csrf string
	key  string // when set, Authorization: Bearer is used instead of cookies
}

func newAPIClient(t *testing.T) *apiClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &apiClient{t: t, c: &http.Client{Jar: jar}}
}

func adminPassword(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(repoRoot(t) + "/.env")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "SBC_BOOTSTRAP_ADMIN_PASSWORD=") {
			return strings.TrimPrefix(line, "SBC_BOOTSTRAP_ADMIN_PASSWORD=")
		}
	}
	return ""
}

func (a *apiClient) login(email, password string) (int, map[string]any) {
	code, body := a.do("POST", "/auth/login", map[string]string{"email": email, "password": password})
	if code == 200 {
		a.csrf, _ = body["csrf_token"].(string)
	}
	return code, body
}

func (a *apiClient) do(method, path string, body any) (int, map[string]any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, adminBase+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if a.key != "" {
		req.Header.Set("Authorization", "Bearer "+a.key)
	} else if a.csrf != "" {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	resp, err := a.c.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	switch {
	case len(raw) > 0 && raw[0] == '{':
		_ = json.Unmarshal(raw, &out)
	case len(raw) > 0 && raw[0] == '[':
		var arr []any
		_ = json.Unmarshal(raw, &arr)
		out["items"] = arr
	case len(raw) > 0:
		out["raw"] = string(raw)
	}
	return resp.StatusCode, out
}

func (a *apiClient) upload(path, content string) (int, map[string]any) {
	a.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "rates.csv")
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	req, _ := http.NewRequestWithContext(context.Background(), "POST", adminBase+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if a.key != "" {
		req.Header.Set("Authorization", "Bearer "+a.key)
	} else {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	resp, err := a.c.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (a *apiClient) mustOK(code int, body map[string]any, what string) {
	a.t.Helper()
	if code < 200 || code >= 300 {
		a.t.Fatalf("%s: http %d %v", what, code, body)
	}
}

// ok is mustOK for the two-value call form: a.ok(a.do(...)).
func (a *apiClient) ok(code int, body map[string]any) {
	a.t.Helper()
	a.mustOK(code, body, "request")
}

func items(body map[string]any) []map[string]any {
	var out []map[string]any
	if arr, ok := body["items"].([]any); ok {
		for _, x := range arr {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func idOf(t *testing.T, body map[string]any) string {
	t.Helper()
	if id, ok := body["id"].(string); ok {
		return id
	}
	t.Fatalf("no id in %v", body)
	return ""
}

func findByName(t *testing.T, a *apiClient, path, name string) string {
	t.Helper()
	code, body := a.do("GET", fmt.Sprintf("%s?search=%s&per_page=100", path, name), nil)
	a.mustOK(code, body, "list "+path)
	for _, it := range items(body) {
		if it["name"] == name {
			return it["id"].(string)
		}
	}
	t.Fatalf("%s %q not found", path, name)
	return ""
}
