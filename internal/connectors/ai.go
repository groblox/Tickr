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
	ProviderAnthropic  = "anthropic"
	ProviderOpenAI     = "openai"
	ProviderOpenRouter = "openrouter"
	ProviderLocal      = "local" // a local OpenAI-compatible server, e.g. llama.cpp's llama-server
)

// DefaultModel returns a sensible default model per provider.
func DefaultModel(provider string) string {
	switch provider {
	case ProviderOpenAI:
		return "gpt-4o-mini"
	case ProviderOpenRouter:
		return "openai/gpt-4o-mini"
	case ProviderLocal:
		return "local-model" // llama.cpp ignores this and serves whatever it loaded at startup
	default:
		return "claude-sonnet-5"
	}
}

// AIKey returns the configured key for a provider, falling back to the
// ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY environment
// variables. A local llama.cpp server usually needs no key at all.
func AIKey(cfg config.AI, provider string) string {
	switch provider {
	case ProviderOpenAI:
		if cfg.OpenAIKey != "" {
			return cfg.OpenAIKey
		}
		return os.Getenv("OPENAI_API_KEY")
	case ProviderOpenRouter:
		if cfg.OpenRouterKey != "" {
			return cfg.OpenRouterKey
		}
		return os.Getenv("OPENROUTER_API_KEY")
	case ProviderLocal:
		if cfg.LocalKey != "" {
			return cfg.LocalKey
		}
		return os.Getenv("LLAMACPP_API_KEY")
	default:
		if cfg.AnthropicKey != "" {
			return cfg.AnthropicKey
		}
		return os.Getenv("ANTHROPIC_API_KEY")
	}
}

// baseURL returns the endpoint an OpenAI-compatible provider talks to.
func baseURL(cfg config.AI, provider string) string {
	switch provider {
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	case ProviderLocal:
		if cfg.LocalBaseURL != "" {
			return strings.TrimRight(cfg.LocalBaseURL, "/")
		}
		return "http://localhost:8080/v1" // llama.cpp's llama-server default
	default:
		if cfg.OpenAIBaseURL != "" {
			return strings.TrimRight(cfg.OpenAIBaseURL, "/")
		}
		return "https://api.openai.com/v1"
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
	if key == "" && req.Provider != ProviderLocal {
		return "", fmt.Errorf("no API key for %s: add one on the Connectors tab or set the environment variable", req.Provider)
	}
	if req.Model == "" {
		req.Model = DefaultModel(req.Provider)
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 300
	}
	switch req.Provider {
	case ProviderOpenAI, ProviderOpenRouter, ProviderLocal:
		return openAICompatComplete(ctx, req.Provider, baseURL(cfg, req.Provider), key, req)
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

// openAICompatComplete talks to any server implementing the OpenAI chat
// completions API: real OpenAI, OpenRouter, and local servers such as
// llama.cpp's llama-server, Ollama or LM Studio.
func openAICompatComplete(ctx context.Context, provider, base, key string, req AIRequest) (string, error) {
	msgs := []map[string]string{}
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": req.Prompt})
	payload := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		// Both keys are sent since servers disagree on which they read:
		// OpenAI's newer models want max_completion_tokens, while OpenRouter,
		// llama.cpp and most other OpenAI-compatible servers still expect the
		// older max_tokens. Unrecognized fields are ignored, so this is safe
		// everywhere.
		"max_tokens":            req.MaxTokens,
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
	headers := map[string]string{}
	if key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	if err := httpx.PostJSON(ctx, base+"/chat/completions", payload, headers, &res); err != nil {
		return "", fmt.Errorf("%s: %w", provider, err)
	}
	if res.Error != nil {
		return "", fmt.Errorf("%s: %s", provider, res.Error.Message)
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("%s: empty response", provider)
	}
	return strings.TrimSpace(res.Choices[0].Message.Content), nil
}
