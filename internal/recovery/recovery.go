package recovery

import (
	"fmt"
	"time"
)

type FailureScenario string

const (
	ScenarioTrustPromptUnresolved FailureScenario = "trust_prompt_unresolved"
	ScenarioPromptMisdelivery     FailureScenario = "prompt_misdelivery"
	ScenarioStaleBranch           FailureScenario = "stale_branch"
	ScenarioCompileRedCrossCrate  FailureScenario = "compile_red_cross_crate"
	ScenarioMCPHandshakeFailure   FailureScenario = "mcp_handshake_failure"
	ScenarioPartialPluginStartup  FailureScenario = "partial_plugin_startup"
	ScenarioProviderFailure       FailureScenario = "provider_failure"
)

type StepKind string

const (
	StepAcceptTrustPrompt StepKind = "accept_trust_prompt"
	StepRedirectPrompt    StepKind = "redirect_prompt_to_agent"
	StepRebaseBranch      StepKind = "rebase_branch"
	StepCleanBuild        StepKind = "clean_build"
	StepRetryMCPHandshake StepKind = "retry_mcp_handshake"
	StepRestartPlugin     StepKind = "restart_plugin"
	StepRestartWorker     StepKind = "restart_worker"
	StepEscalateToHuman   StepKind = "escalate_to_human"
)

type EscalationPolicy string

const (
	EscalateAlertHuman  EscalationPolicy = "alert_human"
	EscalateLogContinue EscalationPolicy = "log_and_continue"
	EscalateAbort       EscalationPolicy = "abort"
)

type ResultKind string

const (
	ResultRecovered          ResultKind = "recovered"
	ResultPartialRecovery    ResultKind = "partial_recovery"
	ResultEscalationRequired ResultKind = "escalation_required"
)

type EventKind string

const (
	EventRecoveryAttempted EventKind = "recovery_attempted"
	EventRecoverySucceeded EventKind = "recovery_succeeded"
	EventRecoveryFailed    EventKind = "recovery_failed"
	EventEscalated         EventKind = "escalated"
)

type Step struct {
	Kind      StepKind `json:"kind"`
	TimeoutMS uint64   `json:"timeout_ms,omitempty"`
	Name      string   `json:"name,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

type Recipe struct {
	Scenario         FailureScenario  `json:"scenario"`
	Steps            []Step           `json:"steps"`
	MaxAttempts      uint32           `json:"max_attempts"`
	EscalationPolicy EscalationPolicy `json:"escalation_policy"`
}

type Result struct {
	Kind       ResultKind `json:"kind"`
	StepsTaken uint32     `json:"steps_taken,omitempty"`
	Recovered  []Step     `json:"recovered,omitempty"`
	Remaining  []Step     `json:"remaining,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

type Event struct {
	Kind      EventKind       `json:"kind"`
	Scenario  FailureScenario `json:"scenario,omitempty"`
	Recipe    Recipe          `json:"recipe,omitempty"`
	Result    Result          `json:"result,omitempty"`
	Timestamp int64           `json:"timestamp"`
}

type Context struct {
	Attempts   map[FailureScenario]uint32 `json:"attempts"`
	Events     []Event                    `json:"events"`
	FailAtStep int                        `json:"-"`
}

type StepExecutor func(step Step) error

func NewContext() Context {
	return Context{
		Attempts:   map[FailureScenario]uint32{},
		Events:     nil,
		FailAtStep: -1,
	}
}

func RecipeFor(scenario FailureScenario) Recipe {
	switch scenario {
	case ScenarioTrustPromptUnresolved:
		return Recipe{
			Scenario:         scenario,
			Steps:            []Step{{Kind: StepAcceptTrustPrompt}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateAlertHuman,
		}
	case ScenarioPromptMisdelivery:
		return Recipe{
			Scenario:         scenario,
			Steps:            []Step{{Kind: StepRedirectPrompt}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateAlertHuman,
		}
	case ScenarioStaleBranch:
		return Recipe{
			Scenario:         scenario,
			Steps:            []Step{{Kind: StepRebaseBranch}, {Kind: StepCleanBuild}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateAlertHuman,
		}
	case ScenarioCompileRedCrossCrate:
		return Recipe{
			Scenario:         scenario,
			Steps:            []Step{{Kind: StepCleanBuild}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateAlertHuman,
		}
	case ScenarioMCPHandshakeFailure:
		return Recipe{
			Scenario:         scenario,
			Steps:            []Step{{Kind: StepRetryMCPHandshake, TimeoutMS: 5000}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateAbort,
		}
	case ScenarioPartialPluginStartup:
		return Recipe{
			Scenario:         scenario,
			Steps:            []Step{{Kind: StepRestartPlugin, Name: "stalled"}, {Kind: StepRetryMCPHandshake, TimeoutMS: 3000}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateLogContinue,
		}
	default:
		return Recipe{
			Scenario:         ScenarioProviderFailure,
			Steps:            []Step{{Kind: StepRestartWorker}},
			MaxAttempts:      1,
			EscalationPolicy: EscalateAlertHuman,
		}
	}
}

func ScenarioForFailureKind(kind string) FailureScenario {
	switch kind {
	case "trust_gate":
		return ScenarioTrustPromptUnresolved
	case "prompt_delivery":
		return ScenarioPromptMisdelivery
	case "protocol":
		return ScenarioMCPHandshakeFailure
	default:
		return ScenarioProviderFailure
	}
}

func Attempt(ctx *Context, scenario FailureScenario, executor StepExecutor) Result {
	if ctx.Attempts == nil {
		ctx.Attempts = map[FailureScenario]uint32{}
	}
	recipe := RecipeFor(scenario)
	if ctx.Attempts[scenario] >= recipe.MaxAttempts {
		result := Result{
			Kind:   ResultEscalationRequired,
			Reason: fmt.Sprintf("max recovery attempts (%d) exceeded for %s", recipe.MaxAttempts, scenario),
		}
		recordAttempt(ctx, scenario, recipe, result)
		recordEvent(ctx, EventEscalated, scenario, recipe, result)
		return result
	}
	ctx.Attempts[scenario]++
	failAtStep := ctx.FailAtStep
	if failAtStep < 0 {
		failAtStep = len(recipe.Steps) + 1
	}
	recovered := make([]Step, 0, len(recipe.Steps))
	for index, step := range recipe.Steps {
		if index == failAtStep {
			result := partialOrEscalated(recipe, recovered, index, "recovery step failed")
			recordAttempt(ctx, scenario, recipe, result)
			recordTerminalEvent(ctx, scenario, recipe, result)
			return result
		}
		if executor != nil {
			if err := executor(step); err != nil {
				result := partialOrEscalated(recipe, recovered, index, err.Error())
				recordAttempt(ctx, scenario, recipe, result)
				recordTerminalEvent(ctx, scenario, recipe, result)
				return result
			}
		}
		recovered = append(recovered, step)
	}
	result := Result{Kind: ResultRecovered, StepsTaken: uint32(len(recipe.Steps))}
	recordAttempt(ctx, scenario, recipe, result)
	recordEvent(ctx, EventRecoverySucceeded, scenario, recipe, result)
	return result
}

func partialOrEscalated(recipe Recipe, recovered []Step, failedIndex int, reason string) Result {
	if len(recovered) == 0 {
		return Result{
			Kind:   ResultEscalationRequired,
			Reason: reason,
		}
	}
	return Result{
		Kind:      ResultPartialRecovery,
		Recovered: append([]Step(nil), recovered...),
		Remaining: append([]Step(nil), recipe.Steps[failedIndex:]...),
		Reason:    reason,
	}
}

func recordAttempt(ctx *Context, scenario FailureScenario, recipe Recipe, result Result) {
	recordEvent(ctx, EventRecoveryAttempted, scenario, recipe, result)
}

func recordTerminalEvent(ctx *Context, scenario FailureScenario, recipe Recipe, result Result) {
	switch result.Kind {
	case ResultRecovered:
		recordEvent(ctx, EventRecoverySucceeded, scenario, recipe, result)
	case ResultPartialRecovery:
		recordEvent(ctx, EventRecoveryFailed, scenario, recipe, result)
	default:
		recordEvent(ctx, EventEscalated, scenario, recipe, result)
	}
}

func recordEvent(ctx *Context, kind EventKind, scenario FailureScenario, recipe Recipe, result Result) {
	ctx.Events = append(ctx.Events, Event{
		Kind:      kind,
		Scenario:  scenario,
		Recipe:    recipe,
		Result:    result,
		Timestamp: time.Now().Unix(),
	})
}
