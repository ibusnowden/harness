package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/oauth"
	workerstate "ascaris/internal/state"
)

func TestLoginUsesLoopbackCallback(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	callbackPort := 4545
	if err := os.MkdirAll(filepath.Join(root, ".ascaris"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configJSON := fmt.Sprintf(`{"oauth":{"clientId":"runtime-client","authorizeUrl":"https://console.test/oauth/authorize","tokenUrl":"https://console.test/oauth/token","callbackPort":%d,"scopes":["org:read"]}}`, callbackPort)
	if err := os.WriteFile(filepath.Join(root, ".ascaris", "settings.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write oauth config: %v", err)
	}
	previousOpener := browserOpener
	defer func() { browserOpener = previousOpener }()
	browserOpener = func(url string) error {
		if !strings.Contains(url, "response_type=code") || !strings.Contains(url, fmt.Sprintf("redirect_uri=%s", urlQueryEscape("http://localhost:4545/callback"))) {
			t.Fatalf("unexpected authorize url: %s", url)
		}
		return nil
	}
	previousState := oauthStateGenerator
	defer func() { oauthStateGenerator = previousState }()
	oauthStateGenerator = func() (string, error) { return "state-from-test", nil }
	previousWait := oauthWaitForCallback
	defer func() { oauthWaitForCallback = previousWait }()
	oauthWaitForCallback = func(_ context.Context, port int) (oauth.CallbackParams, error) {
		if port != callbackPort {
			t.Fatalf("unexpected callback port: %d", port)
		}
		return oauth.CallbackParams{Code: "auth-code", State: "state-from-test"}, nil
	}
	previousExchange := oauthCodeExchanger
	defer func() { oauthCodeExchanger = previousExchange }()
	oauthCodeExchanger = func(_ context.Context, _ *http.Client, _ *config.OAuthSettings, code, verifier, redirectURI string) (oauth.TokenSet, error) {
		if code != "auth-code" || verifier == "" || redirectURI != "http://localhost:4545/callback" {
			t.Fatalf("unexpected exchange inputs: code=%q verifier=%q redirect_uri=%q", code, verifier, redirectURI)
		}
		return oauth.TokenSet{AccessToken: "oauth-access", RefreshToken: "oauth-refresh", ExpiresAt: 1234}, nil
	}

	code, stdout, stderr := runCLI(t, root, "--output-format=json", "login")
	if code != 0 {
		t.Fatalf("login failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	payload := parseJSONOutput(t, stdout)
	if expectString(t, payload["kind"]) != "login" {
		t.Fatalf("unexpected login payload: %#v", payload)
	}
	credentials, err := oauth.LoadCredentials(configHome)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if credentials == nil || credentials.AccessToken != "oauth-access" {
		t.Fatalf("expected saved credentials, got %#v", credentials)
	}
}

func TestLivePromptRecoversFromProviderDegradedCompletion(t *testing.T) {
	var attempts atomic.Int32
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return sseResponse(strings.Join([]string{
				"event: message_start",
				`data: {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":11,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
				"",
				"event: message_delta",
				`data: {"type":"message_delta","delta":{"stop_reason":"unknown","stop_sequence":null},"usage":{"input_tokens":11,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}`,
				"",
				"event: message_stop",
				`data: {"type":"message_stop"}`,
				"",
			}, "\n")), nil
		}
		return sseResponse(finalTextSSEForTest("provider recovery complete")), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	code, stdout, stderr := runCLI(t, root, "--model", "sonnet", "--output-format=json", "prompt", "recover provider")
	if code != 0 {
		t.Fatalf("prompt failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	response := parseJSONOutput(t, stdout)
	if !strings.Contains(expectString(t, response["message"]), "provider recovery complete") {
		t.Fatalf("unexpected response payload: %#v", response)
	}
	snapshot, err := workerstate.Load(root)
	if err != nil {
		t.Fatalf("load worker state: %v", err)
	}
	if len(snapshot.Workers) != 1 || len(snapshot.Workers[0].RecoveryEvents) == 0 {
		t.Fatalf("expected provider recovery events, got %#v", snapshot)
	}
}

func TestLivePromptPreflightsStaleBranchRecovery(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(mockPromptTransport("stale recovery complete"))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	runGitCommand(t, root, "init", "--quiet", "-b", "main")
	runGitCommand(t, root, "config", "user.email", "tests@example.com")
	runGitCommand(t, root, "config", "user.name", "Ascaris Tests")
	writeGitFile(t, root, "shared.txt", "initial\n")
	runGitCommand(t, root, "add", ".")
	runGitCommand(t, root, "commit", "-m", "initial", "--quiet")
	runGitCommand(t, root, "checkout", "-b", "topic")
	runGitCommand(t, root, "checkout", "main")
	writeGitFile(t, root, "shared.txt", "main fix\n")
	runGitCommand(t, root, "add", ".")
	runGitCommand(t, root, "commit", "-m", "fix: update main", "--quiet")
	runGitCommand(t, root, "checkout", "topic")

	code, stdout, stderr := runCLI(t, root, "--model", "sonnet", "--output-format=json", "prompt", "recover stale branch")
	if code != 0 {
		t.Fatalf("prompt failed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	response := parseJSONOutput(t, stdout)
	if !strings.Contains(expectString(t, response["message"]), "stale recovery complete") {
		t.Fatalf("unexpected response payload: %#v", response)
	}
	snapshot, err := workerstate.Load(root)
	if err != nil {
		t.Fatalf("load worker state: %v", err)
	}
	if len(snapshot.Workers) != 1 || len(snapshot.Workers[0].RecoveryEvents) == 0 {
		t.Fatalf("expected stale branch recovery events, got %#v", snapshot)
	}
	found := false
	for _, event := range snapshot.Workers[0].RecoveryEvents {
		if event.Scenario == "stale_branch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stale_branch recovery event, got %#v", snapshot.Workers[0].RecoveryEvents)
	}
}

func mockPromptTransport(finalText string) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		return sseResponse(finalTextSSEForTest(finalText)), nil
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func urlQueryEscape(value string) string {
	var buffer bytes.Buffer
	for _, ch := range []byte(value) {
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-', ch == '_', ch == '.', ch == '~':
			buffer.WriteByte(ch)
		default:
			buffer.WriteString(fmt.Sprintf("%%%02X", ch))
		}
	}
	return buffer.String()
}

func runGitCommand(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func writeGitFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
