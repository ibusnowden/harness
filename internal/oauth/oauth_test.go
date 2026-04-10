package oauth

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ascaris/internal/config"
)

func TestCredentialsRoundTripAndClear(t *testing.T) {
	configHome := t.TempDir()
	token := TokenSet{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    1234,
		Scopes:       []string{"repo:read"},
	}
	if err := SaveCredentials(configHome, token); err != nil {
		t.Fatalf("save credentials: %v", err)
	}
	loaded, err := LoadCredentials(configHome)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if loaded == nil || loaded.AccessToken != token.AccessToken || loaded.RefreshToken != token.RefreshToken {
		t.Fatalf("unexpected token: %#v", loaded)
	}
	if err := ClearCredentials(configHome); err != nil {
		t.Fatalf("clear credentials: %v", err)
	}
	cleared, err := LoadCredentials(configHome)
	if err != nil {
		t.Fatalf("load cleared credentials: %v", err)
	}
	if cleared != nil {
		t.Fatalf("expected cleared credentials, got %#v", cleared)
	}
	if got := CredentialsPath(configHome); got != filepath.Join(configHome, "credentials.json") {
		t.Fatalf("unexpected credentials path: %s", got)
	}
}

func TestAuthorizationAndCallbackHelpers(t *testing.T) {
	settings := &config.OAuthSettings{
		ClientID:          "client-id",
		AuthorizeURL:      "https://console.test/oauth/authorize",
		TokenURL:          "https://console.test/oauth/token",
		ManualRedirectURL: "https://console.test/oauth/callback",
		Scopes:            []string{"repo:read", "user:write"},
	}
	pkce, err := GeneratePKCEPair()
	if err != nil {
		t.Fatalf("generate pkce: %v", err)
	}
	request := BuildAuthorizationRequest(settings, LoopbackRedirectURI(4545), "state-1", pkce)
	url := request.URL()
	for _, fragment := range []string{"response_type=code", "client_id=client-id", "state=state-1", "code_challenge_method=S256"} {
		if !strings.Contains(url, fragment) {
			t.Fatalf("expected %q in authorize url: %s", fragment, url)
		}
	}
	params, err := ParseCallbackRequestTarget("/callback?code=abc&state=state-1")
	if err != nil {
		t.Fatalf("parse callback target: %v", err)
	}
	if params.Code != "abc" || params.State != "state-1" {
		t.Fatalf("unexpected callback params: %#v", params)
	}
	absolute, err := ParseCallbackRequestTarget("http://localhost:4545/callback?code=abc&state=state-1")
	if err != nil {
		t.Fatalf("parse absolute callback target: %v", err)
	}
	if absolute.Code != "abc" || absolute.State != "state-1" {
		t.Fatalf("unexpected absolute callback params: %#v", absolute)
	}
}

func TestWaitForCallback(t *testing.T) {
	resultCh := make(chan CallbackParams, 1)
	errCh := make(chan error, 1)
	server, client := net.Pipe()
	go func() {
		defer server.Close()
		params, err := readCallback(server)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- params
	}()
	go func() {
		defer client.Close()
		_, _ = io.WriteString(client, "GET /callback?code=abc123&state=state-1 HTTP/1.1\r\nHost: localhost\r\n\r\n")
		body, _ := io.ReadAll(client)
		if !strings.Contains(string(body), "Ascaris OAuth login succeeded") {
			errCh <- fmt.Errorf("unexpected callback response body: %q", string(body))
		}
	}()
	select {
	case err := <-errCh:
		t.Fatalf("wait for callback: %v", err)
	case params := <-resultCh:
		if params.Code != "abc123" || params.State != "state-1" {
			t.Fatalf("unexpected callback params: %#v", params)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for callback result")
	}
}

func TestExchangeAndRefreshToken(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		payload := `{"access_token":"access-1","refresh_token":"refresh-1","expires_in":60,"scope":"repo:read"}`
		if r.Form.Get("grant_type") == "refresh_token" {
			payload = `{"access_token":"access-2","expires_in":60}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})}
	settings := &config.OAuthSettings{
		ClientID:          "client-id",
		AuthorizeURL:      "https://console.test/oauth/authorize",
		TokenURL:          "https://console.test/oauth/token",
		ManualRedirectURL: "https://console.test/oauth/callback",
		Scopes:            []string{"repo:read"},
	}
	token, err := ExchangeCode(context.Background(), client, settings, "code-1", "verifier-1", LoopbackRedirectURI(4545))
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if token.AccessToken != "access-1" || token.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected exchange token: %#v", token)
	}
	refreshed, err := RefreshToken(context.Background(), client, settings, token)
	if err != nil {
		t.Fatalf("refresh token: %v", err)
	}
	if refreshed.AccessToken != "access-2" || refreshed.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected refreshed token: %#v", refreshed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
