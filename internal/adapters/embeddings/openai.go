package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const DefaultOpenAIEmbeddingModel = "text-embedding-3-small"

// OpenAIProvider is the first cloud embedding adapter. It stays behind ports.EmbeddingProvider so local providers can be added later.
type OpenAIProvider struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client
}

// NewOpenAIProvider accepts a custom base URL for OpenAI-compatible gateways and tests.
func NewOpenAIProvider(apiKey, model, baseURL string, client *http.Client) (*OpenAIProvider, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY is required for openai embeddings")
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultOpenAIEmbeddingModel
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OpenAIProvider{APIKey: apiKey, Model: model, BaseURL: strings.TrimRight(baseURL, "/"), Client: client}, nil
}

func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("embedding text is required")
	}
	body, err := json.Marshal(map[string]any{"model": p.Model, "input": text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai embeddings status %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, errors.New("openai embeddings response contained no vector")
	}
	return out.Data[0].Embedding, nil
}
