package embeddings_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/embeddings"
)

func TestOpenAIProviderEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()
	provider, err := embeddings.NewOpenAIProvider("test-key", "test-model", server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	vector, err := provider.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vector) != 3 || vector[0] != 0.1 {
		t.Fatalf("vector = %#v", vector)
	}
}

func TestOpenAIProviderRequiresKey(t *testing.T) {
	if _, err := embeddings.NewOpenAIProvider("", "", "", nil); err == nil {
		t.Fatal("NewOpenAIProvider() error = nil, want error")
	}
}
