package recovery

import "testing"

func TestAttemptRecoveryThenEscalate(t *testing.T) {
	ctx := NewContext()
	result := Attempt(&ctx, ScenarioProviderFailure, nil)
	if result.Kind != ResultRecovered {
		t.Fatalf("expected recovered, got %#v", result)
	}
	if len(ctx.Events) != 2 {
		t.Fatalf("expected one recovery event, got %#v", ctx.Events)
	}
	result = Attempt(&ctx, ScenarioProviderFailure, nil)
	if result.Kind != ResultEscalationRequired {
		t.Fatalf("expected escalation after max attempts, got %#v", result)
	}
}

func TestPartialRecovery(t *testing.T) {
	ctx := NewContext()
	ctx.FailAtStep = 1
	result := Attempt(&ctx, ScenarioPartialPluginStartup, nil)
	if result.Kind != ResultPartialRecovery {
		t.Fatalf("expected partial recovery, got %#v", result)
	}
	if len(result.Recovered) != 1 || len(result.Remaining) != 1 {
		t.Fatalf("unexpected partial recovery result: %#v", result)
	}
}

func TestScenarioForFailureKind(t *testing.T) {
	if ScenarioForFailureKind("trust_gate") != ScenarioTrustPromptUnresolved {
		t.Fatalf("trust gate should map to trust prompt scenario")
	}
	if ScenarioForFailureKind("prompt_delivery") != ScenarioPromptMisdelivery {
		t.Fatalf("prompt delivery should map to prompt misdelivery scenario")
	}
	if ScenarioForFailureKind("protocol") != ScenarioMCPHandshakeFailure {
		t.Fatalf("protocol should map to mcp handshake scenario")
	}
	if ScenarioForFailureKind("provider") != ScenarioProviderFailure {
		t.Fatalf("provider should map to provider scenario")
	}
}
