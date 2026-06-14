package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultOllamaEmbeddingModel = "nomic-embed-text"
	DefaultOllamaBaseURL        = "http://127.0.0.1:11434"
)

// OllamaProvider generates embeddings through a local Ollama server.
type OllamaProvider struct {
	Model   string
	BaseURL string
	Client  *http.Client
}

func NewOllamaProvider(model, baseURL string, client *http.Client) (*OllamaProvider, error) {
	if strings.TrimSpace(model) == "" {
		model = DefaultOllamaEmbeddingModel
	}
	baseURL = normalizeOllamaBaseURL(baseURL)
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &OllamaProvider{Model: strings.TrimSpace(model), BaseURL: baseURL, Client: client}, nil
}

func (p *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("embedding text is required")
	}
	body, err := json.Marshal(map[string]any{"model": p.Model, "input": text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama embeddings status %d", resp.StatusCode)
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		Embedding  []float32   `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) > 0 && len(out.Embeddings[0]) > 0 {
		return out.Embeddings[0], nil
	}
	if len(out.Embedding) > 0 {
		return out.Embedding, nil
	}
	return nil, errors.New("ollama embeddings response contained no vector")
}

func normalizeOllamaBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultOllamaBaseURL
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(value, "/")
	}
	return strings.TrimRight(parsed.String(), "/")
}
