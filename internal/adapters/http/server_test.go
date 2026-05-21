package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	httpapi "github.com/priyavratuniyal/tuskbase/internal/adapters/http"
	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/app"
)

func TestHandlers(t *testing.T) {
	handler, closeStore := newHandler(t)
	defer closeStore()

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d", health.Code)
	}

	repo := newRepo(t)
	attachBody := map[string]any{"repo_path": repo}
	attachResp := doJSON(t, handler, http.MethodPost, "/v1/workspaces/attach", attachBody)
	if attachResp.Code != http.StatusOK {
		t.Fatalf("attach status = %d body = %s", attachResp.Code, attachResp.Body.String())
	}
	var attached app.AttachOutput
	decodeResponse(t, attachResp, &attached)

	rememberBody := map[string]any{
		"workspace_id": attached.Workspace.ID,
		"actor":        map[string]any{"kind": "agent", "name": "codex"},
		"type":         "architecture",
		"title":        "Use SQLite first",
		"outcome":      "Use SQLite for local decisions.",
		"rationale":    "SQLite keeps the first local agent memory loop durable without requiring any separate service.",
		"confidence":   0.9,
		"alternatives": []any{map[string]any{"title": "Use Postgres first", "reason": "Deferred until the store port is stable."}},
		"evidence":     []any{map[string]any{"kind": "doc", "uri": "README.md", "snippet": "Local-first repo memory."}},
	}
	rememberResp := doJSON(t, handler, http.MethodPost, "/v1/decisions", rememberBody)
	if rememberResp.Code != http.StatusCreated {
		t.Fatalf("remember status = %d body = %s", rememberResp.Code, rememberResp.Body.String())
	}
	var remembered app.RememberOutput
	decodeResponse(t, rememberResp, &remembered)
	if remembered.Decision.CompletenessScore == 0 {
		t.Fatal("remember response did not include completeness score")
	}

	lookupResp := doJSON(t, handler, http.MethodPost, "/v1/lookup", map[string]any{
		"workspace_id": attached.Workspace.ID,
		"query":        "SQLite decisions",
	})
	if lookupResp.Code != http.StatusOK {
		t.Fatalf("lookup status = %d body = %s", lookupResp.Code, lookupResp.Body.String())
	}

	recentReq := httptest.NewRequest(http.MethodGet, "/v1/workspaces/"+attached.Workspace.ID+"/decisions/recent", nil)
	recentResp := httptest.NewRecorder()
	handler.ServeHTTP(recentResp, recentReq)
	if recentResp.Code != http.StatusOK {
		t.Fatalf("recent status = %d body = %s", recentResp.Code, recentResp.Body.String())
	}
}

func newHandler(t *testing.T) (http.Handler, func()) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	return httpapi.NewServer(service), func() { _ = store.Close() }
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func decodeResponse(t *testing.T, resp *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# HTTP Repo\n\nLocal docs."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	return root
}
