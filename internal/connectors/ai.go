package connectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tickr/internal/config"
	"tickr/internal/httpx"
)

// AI providers.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// DefaultModel returns a sensible default model per provider.
func DefaultModel(provider string) string {
	if provider == ProviderOpenAI {
		return "gpt-4o-mini"
	}
	return "claude-sonnet-5"
}

// AIKey returns the configured key for a provider, falling back to the
// ANTHROPIC_API_KEY / OPENAI_API_KEY environment variables.
func AIKey(cfg config.AI, provider string) string {
	switch provider {
	case ProviderOpenAI:
		if cfg.OpenAIKey != "" {
			return cfg.OpenAIKey
		}
		return os.Getenv("OPENAI_API_KEY")
	default:
		if cfg.AnthropicKey != "" {
			return cfg.AnthropicKey
		}
		return os.Getenv("ANTHROPIC_API_KEY")
	}
}

// AIRequest is one completion call.
type AIRequest struct {
	Provider    string
	Model       string
	System      string
	Prompt      string
	MaxTokens   int
	Temperature float64
}

// Complete sends a single-turn prompt and returns the text reply.
func Complete(ctx context.Context, cfg config.AI, req AIRequest) (string, error) {
	key := AIKey(cfg, req.Provider)
	if key == "" {
		return "", fmt.Errorf("no API key for %s: add one on the Connectors tab or set the environment variable", req.Provider)
	}
	if req.Model == "" {
		req.Model = DefaultModel(req.Provider)
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 300
	}
	switch req.Provider {
	case ProviderOpenAI:
		return openAIComplete(ctx, cfg, key, req)
	default:
		return anthropicComplete(ctx, key, req)
	}
}

func anthropicComplete(ctx context.Context, key string, req AIRequest) (string, error) {
	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   []map[string]string{{"role": "user", "content": req.Prompt}},
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	headers := map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}
	if err := httpx.PostJSON(ctx, "https://api.anthropic.com/v1/messages", payload, headers, &res); err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	if res.Error != nil {
		return "", fmt.Errorf("anthropic: %s", res.Error.Message)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func openAIComplete(ctx context.Context, cfg config.AI, key string, req AIRequest) (string, error) {
	base := strings.TrimRight(cfg.OpenAIBaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	msgs := []map[string]string{}
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": req.Prompt})
	payload := map[string]any{
		"model":                 req.Model,
		"messages":              msgs,
		"max_completion_tokens": req.MaxTokens,
	}
	if req.Temperature > 0 {
		payload["temperature"] = req.Temperature
	}
	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	headers := map[string]string{"Authorization": "Bearer " + key}
	if err := httpx.PostJSON(ctx, base+"/chat/completions", payload, headers, &res); err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	if res.Error != nil {
		return "", fmt.Errorf("openai: %s", res.Error.Message)
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("openai: empty response")
	}
	return strings.TrimSpace(res.Choices[0].Message.Content), nil
}
