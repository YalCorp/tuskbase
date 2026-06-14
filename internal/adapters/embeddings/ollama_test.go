package embeddings_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/embeddings"
)

func TestOllamaProviderEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var in struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if in.Model != "all-minilm" || in.Input != "hello" {
			t.Fatalf("request = %#v", in)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"all-minilm","embeddings":[[0.1,0.2,0.3]]}`))
	}))
	defer server.Close()
	provider, err := embeddings.NewOllamaProvider("all-minilm", server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewOllamaProvider() error = %v", err)
	}
	vector, err := provider.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vector) != 3 || vector[0] != 0.1 {
		t.Fatalf("vector = %#v", vector)
	}
}

func TestOllamaProviderEmbedLegacyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":[0.4,0.5]}`))
	}))
	defer server.Close()
	provider, err := embeddings.NewOllamaProvider("", server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewOllamaProvider() error = %v", err)
	}
	vector, err := provider.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vector) != 2 || vector[1] != 0.5 {
		t.Fatalf("vector = %#v", vector)
	}
}

func TestOllamaProviderDefaults(t *testing.T) {
	provider, err := embeddings.NewOllamaProvider("", "", nil)
	if err != nil {
		t.Fatalf("NewOllamaProvider() error = %v", err)
	}
	if provider.Model != embeddings.DefaultOllamaEmbeddingModel {
		t.Fatalf("model = %q", provider.Model)
	}
	if provider.BaseURL != embeddings.DefaultOllamaBaseURL {
		t.Fatalf("base url = %q", provider.BaseURL)
	}
}
