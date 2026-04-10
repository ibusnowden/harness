package oauth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ascaris/internal/config"
)

type TokenSet struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	ExpiresAt    int64    `json:"expires_at,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

type PKCEPair struct {
	Verifier        string
	Challenge       string
	ChallengeMethod string
}

type AuthorizationRequest struct {
	AuthorizeURL        string
	ClientID            string
	RedirectURI         string
	Scopes              []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	ExtraParams         map[string]string
}

type CallbackParams struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

func CredentialsPath(configHome string) string {
	return filepath.Join(configHome, "credentials.json")
}

func LoadCredentials(configHome string) (*TokenSet, error) {
	root, err := readCredentialsRoot(CredentialsPath(configHome))
	if err != nil {
		return nil, err
	}
	value, ok := root["oauth"]
	if !ok {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var token TokenSet
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, nil
	}
	return &token, nil
}

func SaveCredentials(configHome string, token TokenSet) error {
	root, err := readCredentialsRoot(CredentialsPath(configHome))
	if err != nil {
		return err
	}
	root["oauth"] = token
	return writeCredentialsRoot(CredentialsPath(configHome), root)
}

func ClearCredentials(configHome string) error {
	root, err := readCredentialsRoot(CredentialsPath(configHome))
	if err != nil {
		return err
	}
	delete(root, "oauth")
	return writeCredentialsRoot(CredentialsPath(configHome), root)
}

func GeneratePKCEPair() (PKCEPair, error) {
	verifier, err := randomToken(32)
	if err != nil {
		return PKCEPair{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return PKCEPair{
		Verifier:        verifier,
		Challenge:       base64.RawURLEncoding.EncodeToString(sum[:]),
		ChallengeMethod: "S256",
	}, nil
}

func GenerateState() (string, error) {
	return randomToken(32)
}

func LoopbackRedirectURI(port int) string {
	return fmt.Sprintf("http://localhost:%d/callback", port)
}

func RedirectURI(settings *config.OAuthSettings) string {
	if settings == nil {
		return ""
	}
	if strings.TrimSpace(settings.ManualRedirectURL) != "" {
		return strings.TrimSpace(settings.ManualRedirectURL)
	}
	if settings.CallbackPort > 0 {
		return LoopbackRedirectURI(settings.CallbackPort)
	}
	return ""
}

func WaitForCallback(ctx context.Context, port int) (CallbackParams, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return CallbackParams{}, err
	}
	defer listener.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if tcpListener, ok := listener.(*net.TCPListener); ok {
			_ = tcpListener.SetDeadline(deadline)
		}
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	conn, err := listener.Accept()
	if err != nil {
		if ctx.Err() != nil {
			return CallbackParams{}, ctx.Err()
		}
		return CallbackParams{}, err
	}
	defer conn.Close()
	return readCallback(conn)
}

func BuildAuthorizationRequest(settings *config.OAuthSettings, redirectURI, state string, pkce PKCEPair) AuthorizationRequest {
	return AuthorizationRequest{
		AuthorizeURL:        settings.AuthorizeURL,
		ClientID:            settings.ClientID,
		RedirectURI:         redirectURI,
		Scopes:              append([]string(nil), settings.Scopes...),
		State:               state,
		CodeChallenge:       pkce.Challenge,
		CodeChallengeMethod: pkce.ChallengeMethod,
		ExtraParams:         map[string]string{},
	}
}

func (r AuthorizationRequest) URL() string {
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", r.ClientID)
	values.Set("redirect_uri", r.RedirectURI)
	if len(r.Scopes) > 0 {
		values.Set("scope", strings.Join(r.Scopes, " "))
	}
	values.Set("state", r.State)
	values.Set("code_challenge", r.CodeChallenge)
	values.Set("code_challenge_method", r.CodeChallengeMethod)
	for key, value := range r.ExtraParams {
		values.Set(key, value)
	}
	separator := "?"
	if strings.Contains(r.AuthorizeURL, "?") {
		separator = "&"
	}
	return r.AuthorizeURL + separator + values.Encode()
}

func ParseCallbackQuery(query string) (CallbackParams, error) {
	values, err := url.ParseQuery(strings.TrimPrefix(query, "?"))
	if err != nil {
		return CallbackParams{}, err
	}
	return CallbackParams{
		Code:             strings.TrimSpace(values.Get("code")),
		State:            strings.TrimSpace(values.Get("state")),
		Error:            strings.TrimSpace(values.Get("error")),
		ErrorDescription: strings.TrimSpace(values.Get("error_description")),
	}, nil
}

func ParseCallbackRequestTarget(target string) (CallbackParams, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return CallbackParams{}, err
	}
	if parsed.IsAbs() {
		if parsed.Path != "" && parsed.Path != "/callback" {
			return CallbackParams{}, fmt.Errorf("unexpected callback path: %s", parsed.Path)
		}
		return ParseCallbackQuery(parsed.RawQuery)
	}
	if parsed.Path != "" && parsed.Path != "/callback" {
		return CallbackParams{}, fmt.Errorf("unexpected callback path: %s", parsed.Path)
	}
	return ParseCallbackQuery(parsed.RawQuery)
}

func TokenExpired(token *TokenSet) bool {
	if token == nil || token.ExpiresAt == 0 {
		return false
	}
	return token.ExpiresAt <= time.Now().Unix()+30
}

func ExchangeCode(ctx context.Context, httpClient *http.Client, settings *config.OAuthSettings, code, verifier, redirectURI string) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", settings.ClientID)
	form.Set("code_verifier", verifier)
	return tokenRequest(ctx, httpClient, settings.TokenURL, form, TokenSet{})
}

func RefreshToken(ctx context.Context, httpClient *http.Client, settings *config.OAuthSettings, existing TokenSet) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", existing.RefreshToken)
	form.Set("client_id", settings.ClientID)
	if len(settings.Scopes) > 0 {
		form.Set("scope", strings.Join(settings.Scopes, " "))
	}
	return tokenRequest(ctx, httpClient, settings.TokenURL, form, existing)
}

func ResolveSavedToken(ctx context.Context, httpClient *http.Client, configHome string, settings *config.OAuthSettings) (*TokenSet, error) {
	token, err := LoadCredentials(configHome)
	if err != nil || token == nil {
		return token, err
	}
	if !TokenExpired(token) {
		return token, nil
	}
	if settings == nil || strings.TrimSpace(token.RefreshToken) == "" || strings.TrimSpace(settings.TokenURL) == "" {
		return token, nil
	}
	refreshed, err := RefreshToken(ctx, httpClient, settings, *token)
	if err != nil {
		return nil, err
	}
	if err := SaveCredentials(configHome, refreshed); err != nil {
		return nil, err
	}
	return &refreshed, nil
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func tokenRequest(ctx context.Context, httpClient *http.Client, tokenURL string, form url.Values, existing TokenSet) (TokenSet, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	request.Header.Set("content-type", "application/x-www-form-urlencoded")
	response, err := httpClient.Do(request)
	if err != nil {
		return TokenSet{}, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		payload, _ := io.ReadAll(response.Body)
		return TokenSet{}, fmt.Errorf("oauth token request failed: %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var decoded struct {
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		ExpiresIn    any      `json:"expires_in"`
		ExpiresAt    any      `json:"expires_at"`
		Scope        string   `json:"scope"`
		Scopes       []string `json:"scopes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return TokenSet{}, err
	}
	token := TokenSet{
		AccessToken:  strings.TrimSpace(decoded.AccessToken),
		RefreshToken: strings.TrimSpace(decoded.RefreshToken),
		Scopes:       append([]string(nil), decoded.Scopes...),
	}
	if token.RefreshToken == "" {
		token.RefreshToken = existing.RefreshToken
	}
	if len(token.Scopes) == 0 && strings.TrimSpace(decoded.Scope) != "" {
		token.Scopes = strings.Fields(decoded.Scope)
	}
	if token.AccessToken == "" {
		return TokenSet{}, fmt.Errorf("oauth token response is missing access_token")
	}
	token.ExpiresAt = resolveExpiry(decoded.ExpiresAt, decoded.ExpiresIn)
	return token, nil
}

func resolveExpiry(expiresAt, expiresIn any) int64 {
	if value := int64Value(expiresAt); value > 0 {
		return value
	}
	if value := int64Value(expiresIn); value > 0 {
		return time.Now().Unix() + value
	}
	return 0
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func readCredentialsRoot(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

func writeCredentialsRoot(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readCallback(conn net.Conn) (CallbackParams, error) {
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return CallbackParams{}, err
	}
	defer request.Body.Close()
	params, err := ParseCallbackRequestTarget(request.RequestURI)
	if err != nil {
		writeCallbackResponse(conn, http.StatusBadRequest, "Ascaris OAuth login failed. You can close this window.")
		return CallbackParams{}, err
	}
	body := "Ascaris OAuth login succeeded. You can close this window."
	if strings.TrimSpace(params.Error) != "" {
		body = "Ascaris OAuth login failed. You can close this window."
	}
	if err := writeCallbackResponse(conn, http.StatusOK, body); err != nil {
		return CallbackParams{}, err
	}
	return params, nil
}

func writeCallbackResponse(conn net.Conn, status int, body string) error {
	response := fmt.Sprintf(
		"HTTP/1.1 %d %s\r\ncontent-type: text/plain; charset=utf-8\r\ncontent-length: %d\r\nconnection: close\r\n\r\n%s",
		status,
		http.StatusText(status),
		len(body),
		body,
	)
	_, err := io.WriteString(conn, response)
	return err
}
