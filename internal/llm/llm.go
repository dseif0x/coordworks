// Package llm abstracts chat-completion providers so agents can mix models
// from Anthropic, OpenAI and any OpenAI-compatible endpoint.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/dseif0x/coordworks/internal/domain"
)

// Message is a single conversation turn.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// Request is a provider-agnostic completion request.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	MaxTokens int
}

// Usage counts tokens for cost tracking.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Response is the model's reply plus usage.
type Response struct {
	Text  string
	Usage Usage
}

// Client executes completion requests against one provider.
type Client interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

// ProviderConfig is the subset of a domain.Provider a runner needs to talk to
// the model API. The control plane ships it to runners inside job bundles.
type ProviderConfig struct {
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// FromProvider converts a stored provider into a shippable config.
func FromProvider(p *domain.Provider) ProviderConfig {
	return ProviderConfig{Kind: p.Kind, BaseURL: p.BaseURL, APIKey: p.APIKey}
}

// New builds a client for the given provider config.
func New(cfg ProviderConfig) (Client, error) {
	hc := &http.Client{Timeout: 5 * time.Minute}
	switch cfg.Kind {
	case domain.ProviderAnthropic:
		return newAnthropic(cfg, hc), nil
	case domain.ProviderOpenAI, domain.ProviderOpenAICompatible:
		return newOpenAI(cfg, hc), nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q", cfg.Kind)
	}
}

// APIError is a non-2xx response from a provider.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("provider API error: status %d: %s", e.Status, truncate(e.Body, 500))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
