package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"ascaris/internal/config"
	"ascaris/internal/oauth"
)

type ProviderKind string

const (
	ProviderAnthropic  ProviderKind = "anthropic"
	ProviderOpenAI     ProviderKind = "openai"
	ProviderOpenRouter ProviderKind = "openrouter"
	ProviderXAI        ProviderKind = "xai"
)

type ProviderConfig struct {
	AnthropicBaseURL  string
	OpenAIBaseURL     string
	OpenRouterBaseURL string
	PreferredProvider ProviderKind
	XAIBaseURL        string
	ProxyURL          string
	ConfigHome        string
	OAuthSettings     *config.OAuthSettings
}

type MessageClient interface {
	ProviderKind() ProviderKind
	StreamMessage(ctx context.Context, request MessageRequest) (MessageResponse, error)
}

func ConfiguredFromEnv() bool {
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY",
		"OPENAI_BASE_URL",
		"OPENROUTER_API_KEY",
		"OPENROUTER_BASE_URL",
		"XAI_API_KEY",
		"XAI_BASE_URL",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func NewProviderClient(model string, cfg ProviderConfig) (MessageClient, error) {
	switch providerKindForModel(model, cfg) {
	case ProviderXAI:
		apiKey := strings.TrimSpace(os.Getenv("XAI_API_KEY"))
		if apiKey == "" {
			return nil, fmt.Errorf("XAI_API_KEY is required for xAI models")
		}
		baseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("XAI_BASE_URL")), cfg.XAIBaseURL, "https://api.x.ai/v1")
		return &OpenAICompatClient{
			kind:       ProviderXAI,
			baseURL:    baseURL,
			apiKey:     apiKey,
			httpClient: newHTTPClient(cfg.ProxyURL),
		}, nil
	case ProviderOpenRouter:
		apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
		if apiKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY is required for OpenRouter models")
		}
		baseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL")), cfg.OpenRouterBaseURL, "https://openrouter.ai/api/v1")
		return &OpenAICompatClient{
			kind:       ProviderOpenRouter,
			baseURL:    baseURL,
			apiKey:     apiKey,
			httpClient: newHTTPClient(cfg.ProxyURL),
		}, nil
	case ProviderOpenAI:
		apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required for OpenAI-compatible models")
		}
		baseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), cfg.OpenAIBaseURL, "https://api.openai.com/v1")
		return &OpenAICompatClient{
			kind:       ProviderOpenAI,
			baseURL:    baseURL,
			apiKey:     apiKey,
			httpClient: newHTTPClient(cfg.ProxyURL),
		}, nil
	default:
		return NewAnthropicClient(context.Background(), cfg)
	}
}

func ParseProviderKind(raw string) (ProviderKind, error) {
	normalized := ProviderKind(strings.ToLower(strings.TrimSpace(raw)))
	switch normalized {
	case "":
		return "", nil
	case ProviderAnthropic, ProviderOpenAI, ProviderOpenRouter, ProviderXAI:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported provider: %s", raw)
	}
}

func NewAnthropicClientFromEnv(cfg ProviderConfig) (*Client, error) {
	return NewAnthropicClient(context.Background(), cfg)
}

func NewAnthropicClient(ctx context.Context, cfg ProviderConfig) (*Client, error) {
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	if baseURL == "" {
		baseURL = firstNonEmpty(cfg.AnthropicBaseURL, defaultBaseURL)
	}
	client := &Client{
		BaseURL:    baseURL,
		HTTPClient: newHTTPClient(cfg.ProxyURL),
	}
	if apiKey != "" {
		client.APIKey = apiKey
		return client, nil
	}
	if cfg.OAuthSettings != nil && strings.TrimSpace(cfg.ConfigHome) != "" {
		token, err := oauth.ResolveSavedToken(ctx, client.HTTPClient, cfg.ConfigHome, cfg.OAuthSettings)
		if err != nil {
			return nil, err
		}
		if token != nil && strings.TrimSpace(token.AccessToken) != "" {
			client.BearerToken = token.AccessToken
			return client, nil
		}
	}
	return nil, fmt.Errorf("ANTHROPIC_API_KEY or saved OAuth credentials are required for Anthropic models")
}

func providerKindForModel(model string, cfg ProviderConfig) ProviderKind {
	if cfg.PreferredProvider != "" {
		return cfg.PreferredProvider
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case shouldUseOpenRouter(normalized, cfg):
		return ProviderOpenRouter
	case strings.Contains(normalized, "grok"):
		return ProviderXAI
	case strings.HasPrefix(normalized, "gpt"), strings.HasPrefix(normalized, "o1"), strings.HasPrefix(normalized, "o3"), strings.HasPrefix(normalized, "o4"):
		return ProviderOpenAI
	default:
		return ProviderAnthropic
	}
}

func shouldUseOpenRouter(model string, cfg ProviderConfig) bool {
	if !openRouterConfigured(cfg) {
		return false
	}
	return strings.Contains(model, "/")
}

func openRouterConfigured(cfg ProviderConfig) bool {
	return strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "" ||
		strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL")) != "" ||
		strings.TrimSpace(cfg.OpenRouterBaseURL) != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func transportFromProxyURL(proxyURL string) http.RoundTripper {
	if strings.TrimSpace(proxyURL) == "" {
		return nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = http.ProxyURL(parsed)
	return base
}
