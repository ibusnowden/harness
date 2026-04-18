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
	ProviderGoogle     ProviderKind = "google"
	ProviderOpenAI     ProviderKind = "openai"
	ProviderOpenRouter ProviderKind = "openrouter"
	ProviderXAI        ProviderKind = "xai"
)

type ProviderConfig struct {
	AnthropicBaseURL  string
	GoogleBaseURL     string
	OpenAIBaseURL     string
	OpenRouterBaseURL string
	PreferredProvider ProviderKind
	XAIBaseURL        string
	ProxyURL          string
	ConfigHome        string
	OAuthSettings     *config.OAuthSettings
}

type ModelRoute struct {
	Provider     ProviderKind
	DisplayModel string
	RequestModel string
}

type MessageClient interface {
	ProviderKind() ProviderKind
	StreamMessage(ctx context.Context, request MessageRequest) (MessageResponse, error)
	StreamMessageEvents(ctx context.Context, request MessageRequest, emit func(StreamEvent)) (MessageResponse, error)
}

func ConfiguredFromEnv() bool {
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"GOOGLE_API_KEY",
		"GOOGLE_BASE_URL",
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
	route, err := ResolveModelRoute(model, cfg)
	if err != nil {
		return nil, err
	}
	switch route.Provider {
	case ProviderGoogle:
		apiKey := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
		if apiKey == "" {
			return nil, fmt.Errorf("GOOGLE_API_KEY is required for Google Gemini models")
		}
		baseURL := firstNonEmpty(strings.TrimSpace(os.Getenv("GOOGLE_BASE_URL")), cfg.GoogleBaseURL, "https://generativelanguage.googleapis.com/v1beta/openai")
		return &OpenAICompatClient{
			kind:       ProviderGoogle,
			baseURL:    baseURL,
			apiKey:     apiKey,
			httpClient: newHTTPClient(cfg.ProxyURL),
		}, nil
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
	case ProviderAnthropic, ProviderGoogle, ProviderOpenAI, ProviderOpenRouter, ProviderXAI:
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

// AutoDetectProvider returns the ProviderKind that would be used for model
// given the current environment and config, ignoring any explicit preferred
// provider override. Used to show the real provider label in the TUI header.
func AutoDetectProvider(model string, cfg ProviderConfig) ProviderKind {
	cfg.PreferredProvider = ""
	route, err := ResolveModelRoute(model, cfg)
	if err != nil {
		return ""
	}
	return route.Provider
}

func ResolveModelRoute(model string, cfg ProviderConfig) (ModelRoute, error) {
	normalized := strings.TrimSpace(model)
	if normalized == "" {
		return ModelRoute{}, fmt.Errorf("model name is required")
	}
	if strings.Contains(normalized, ":") && !strings.Contains(normalized, "/") {
		return ModelRoute{}, fmt.Errorf("provider-qualified models must use slash syntax, got %q", normalized)
	}
	family := nativeProviderForModel(normalized)
	if cfg.PreferredProvider != "" {
		return resolvePreferredProviderRoute(normalized, family, cfg.PreferredProvider)
	}
	if isOpenRouterSlug(normalized, cfg) {
		if !openRouterConfigured(cfg) {
			return ModelRoute{}, fmt.Errorf("OPENROUTER_API_KEY is required for provider-qualified models such as %q", normalized)
		}
		return ModelRoute{
			Provider:     ProviderOpenRouter,
			DisplayModel: normalized,
			RequestModel: normalized,
		}, nil
	}
	if family == "" {
		return ModelRoute{}, fmt.Errorf("cannot infer provider for model %q; use --provider <name> for direct providers or a slash slug such as openai/%s for OpenRouter", normalized, normalized)
	}
	return ModelRoute{
		Provider:     family,
		DisplayModel: normalized,
		RequestModel: normalized,
	}, nil
}

func resolvePreferredProviderRoute(model string, family, preferred ProviderKind) (ModelRoute, error) {
	switch preferred {
	case ProviderOpenRouter:
		return ModelRoute{
			Provider:     ProviderOpenRouter,
			DisplayModel: model,
			RequestModel: openRouterModelForProvider(model, family),
		}, nil
	case ProviderAnthropic, ProviderGoogle, ProviderOpenAI, ProviderXAI:
		if strings.Contains(model, "/") {
			return ModelRoute{
				Provider:     preferred,
				DisplayModel: model,
				RequestModel: model,
			}, nil
		}
		if family != "" && family != preferred {
			return ModelRoute{}, fmt.Errorf("model %q belongs to provider %s; choose a plain %s model or use --provider openrouter with a slash slug", model, family, preferred)
		}
		return ModelRoute{
			Provider:     preferred,
			DisplayModel: model,
			RequestModel: model,
		}, nil
	default:
		return ModelRoute{
			Provider:     preferred,
			DisplayModel: model,
			RequestModel: model,
		}, nil
	}
}

func nativeProviderForModel(model string) ProviderKind {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(normalized, "claude-"):
		return ProviderAnthropic
	case strings.HasPrefix(normalized, "gemini"):
		return ProviderGoogle
	case strings.HasPrefix(normalized, "grok"):
		return ProviderXAI
	case strings.HasPrefix(normalized, "gpt"), strings.HasPrefix(normalized, "o1"), strings.HasPrefix(normalized, "o3"), strings.HasPrefix(normalized, "o4"):
		return ProviderOpenAI
	default:
		return ""
	}
}

func isOpenRouterSlug(model string, cfg ProviderConfig) bool {
	prefix, bare := splitOpenRouterSlug(model)
	if prefix == "" || bare == "" {
		return false
	}
	switch prefix {
	case "anthropic", "openai", "google", "x-ai", "meta-llama", "qwen", "thudm":
		return true
	default:
		return openRouterConfigured(cfg)
	}
}

func splitOpenRouterSlug(model string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(model), "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
}

func providerForSlugPrefix(prefix string) ProviderKind {
	switch prefix {
	case "anthropic":
		return ProviderAnthropic
	case "openai":
		return ProviderOpenAI
	case "google":
		return ProviderGoogle
	case "x-ai", "xai":
		return ProviderXAI
	default:
		return ProviderOpenRouter
	}
}

func openRouterModelForProvider(model string, family ProviderKind) string {
	switch family {
	case ProviderAnthropic:
		return "anthropic/" + model
	case ProviderGoogle:
		return "google/" + model
	case ProviderOpenAI:
		return "openai/" + model
	case ProviderXAI:
		return "x-ai/" + model
	default:
		return model
	}
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
