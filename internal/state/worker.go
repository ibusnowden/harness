package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ascaris/internal/recovery"
)

type WorkerStatus string

const (
	WorkerSpawning       WorkerStatus = "spawning"
	WorkerTrustRequired  WorkerStatus = "trust_required"
	WorkerReadyForPrompt WorkerStatus = "ready_for_prompt"
	WorkerRunning        WorkerStatus = "running"
	WorkerFinished       WorkerStatus = "finished"
	WorkerFailed         WorkerStatus = "failed"
)

type WorkerFailureKind string

const (
	WorkerFailureTrustGate      WorkerFailureKind = "trust_gate"
	WorkerFailurePromptDelivery WorkerFailureKind = "prompt_delivery"
	WorkerFailureProtocol       WorkerFailureKind = "protocol"
	WorkerFailureProvider       WorkerFailureKind = "provider"
)

type WorkerEventKind string

const (
	EventSpawning          WorkerEventKind = "spawning"
	EventTrustRequired     WorkerEventKind = "trust_required"
	EventTrustResolved     WorkerEventKind = "trust_resolved"
	EventReadyForPrompt    WorkerEventKind = "ready_for_prompt"
	EventPromptMisdelivery WorkerEventKind = "prompt_misdelivery"
	EventPromptReplayArmed WorkerEventKind = "prompt_replay_armed"
	EventRunning           WorkerEventKind = "running"
	EventRestarted         WorkerEventKind = "restarted"
	EventFinished          WorkerEventKind = "finished"
	EventFailed            WorkerEventKind = "failed"
)

type WorkerTrustResolution string

const (
	TrustResolutionAutoAllowlisted WorkerTrustResolution = "auto_allowlisted"
	TrustResolutionManualApproval  WorkerTrustResolution = "manual_approval"
)

type WorkerPromptTarget string

const (
	PromptTargetShell       WorkerPromptTarget = "shell"
	PromptTargetWrongTarget WorkerPromptTarget = "wrong_target"
	PromptTargetUnknown     WorkerPromptTarget = "unknown"
)

type WorkerFailure struct {
	Kind      WorkerFailureKind `json:"kind"`
	Message   string            `json:"message"`
	CreatedAt int64             `json:"created_at"`
}

type WorkerEventPayload struct {
	Type           string                 `json:"type"`
	CWD            string                 `json:"cwd,omitempty"`
	Resolution     *WorkerTrustResolution `json:"resolution,omitempty"`
	PromptPreview  string                 `json:"prompt_preview,omitempty"`
	ObservedTarget WorkerPromptTarget     `json:"observed_target,omitempty"`
	ObservedCWD    *string                `json:"observed_cwd,omitempty"`
	RecoveryArmed  *bool                  `json:"recovery_armed,omitempty"`
}

type WorkerEvent struct {
	Seq       uint64              `json:"seq"`
	Kind      WorkerEventKind     `json:"kind"`
	Status    WorkerStatus        `json:"status"`
	Detail    string              `json:"detail,omitempty"`
	Payload   *WorkerEventPayload `json:"payload,omitempty"`
	Timestamp int64               `json:"timestamp"`
}

type Worker struct {
	WorkerID                     string                              `json:"worker_id"`
	CWD                          string                              `json:"cwd"`
	Status                       WorkerStatus                        `json:"status"`
	TrustAutoResolve             bool                                `json:"trust_auto_resolve"`
	TrustGateCleared             bool                                `json:"trust_gate_cleared"`
	AutoRecoverPromptMisdelivery bool                                `json:"auto_recover_prompt_misdelivery"`
	PromptDeliveryAttempts       uint32                              `json:"prompt_delivery_attempts"`
	PromptInFlight               bool                                `json:"prompt_in_flight"`
	LastPrompt                   string                              `json:"last_prompt,omitempty"`
	ReplayPrompt                 string                              `json:"replay_prompt,omitempty"`
	LastError                    *WorkerFailure                      `json:"last_error,omitempty"`
	RecoveryAttempts             map[recovery.FailureScenario]uint32 `json:"recovery_attempts,omitempty"`
	RecoveryEvents               []recovery.Event                    `json:"recovery_events,omitempty"`
	CreatedAt                    int64                               `json:"created_at"`
	UpdatedAt                    int64                               `json:"updated_at"`
	Events                       []WorkerEvent                       `json:"events"`
}

type WorkerReadySnapshot struct {
	WorkerID          string         `json:"worker_id"`
	Status            WorkerStatus   `json:"status"`
	Ready             bool           `json:"ready"`
	Blocked           bool           `json:"blocked"`
	ReplayPromptReady bool           `json:"replay_prompt_ready"`
	LastError         *WorkerFailure `json:"last_error,omitempty"`
}

type Snapshot struct {
	Workers []Worker `json:"workers"`
}

type Registry struct {
	mu      sync.Mutex
	counter uint64
	workers map[string]Worker
}

func NewRegistry() *Registry {
	return &Registry{workers: map[string]Worker{}}
}

func LoadWorkerRegistry(root string) (*Registry, error) {
	registry := NewRegistry()
	snapshot, err := Load(root)
	if err != nil {
		return nil, err
	}
	registry.Replace(snapshot)
	return registry, nil
}

func SaveWorkerRegistry(root string, registry *Registry) error {
	if registry == nil {
		return nil
	}
	return Save(root, registry.Snapshot())
}

func (r *Registry) Create(cwd string, trustedRoots []string, autoRecoverPromptMisdelivery bool) Worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	now := time.Now().Unix()
	workerID := fmt.Sprintf("worker_%08x_%d", now, r.counter)
	worker := Worker{
		WorkerID:                     workerID,
		CWD:                          cwd,
		Status:                       WorkerSpawning,
		TrustAutoResolve:             matchesTrustedRoot(cwd, trustedRoots),
		AutoRecoverPromptMisdelivery: autoRecoverPromptMisdelivery,
		RecoveryAttempts:             map[recovery.FailureScenario]uint32{},
		CreatedAt:                    now,
		UpdatedAt:                    now,
	}
	pushEvent(&worker, EventSpawning, WorkerSpawning, "worker created", nil)
	r.workers[workerID] = worker
	return worker
}

func (r *Registry) Get(workerID string) (Worker, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	return worker, ok
}

func (r *Registry) Observe(workerID, screenText string) (Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return Worker{}, fmt.Errorf("worker not found: %s", workerID)
	}
	lowered := strings.ToLower(screenText)
	if !worker.TrustGateCleared && detectTrustPrompt(lowered) {
		worker.Status = WorkerTrustRequired
		worker.LastError = &WorkerFailure{
			Kind:      WorkerFailureTrustGate,
			Message:   "worker boot blocked on trust prompt",
			CreatedAt: time.Now().Unix(),
		}
		pushEvent(&worker, EventTrustRequired, WorkerTrustRequired, "trust prompt detected", &WorkerEventPayload{
			Type: "trust_prompt",
			CWD:  worker.CWD,
		})
		if worker.TrustAutoResolve {
			worker.TrustGateCleared = true
			worker.LastError = nil
			worker.Status = WorkerSpawning
			resolution := TrustResolutionAutoAllowlisted
			pushEvent(&worker, EventTrustResolved, WorkerSpawning, "allowlisted repo auto-resolved trust prompt", &WorkerEventPayload{
				Type:       "trust_prompt",
				CWD:        worker.CWD,
				Resolution: &resolution,
			})
		}
		worker.UpdatedAt = time.Now().Unix()
		r.workers[workerID] = worker
		return worker, nil
	}
	if observation := detectPromptMisdelivery(screenText, lowered, worker.LastPrompt, worker.CWD); worker.PromptInFlight && observation != nil {
		preview := promptPreview(worker.LastPrompt)
		message := promptMisdeliveryMessage(*observation, preview, worker.CWD)
		worker.LastError = &WorkerFailure{
			Kind:      WorkerFailurePromptDelivery,
			Message:   message,
			CreatedAt: time.Now().Unix(),
		}
		worker.PromptInFlight = false
		recoveryArmed := false
		pushEvent(&worker, EventPromptMisdelivery, WorkerFailed, observation.detail, &WorkerEventPayload{
			Type:           "prompt_delivery",
			PromptPreview:  preview,
			ObservedTarget: observation.target,
			ObservedCWD:    observation.observedCWD,
			RecoveryArmed:  &recoveryArmed,
		})
		if worker.AutoRecoverPromptMisdelivery && worker.LastPrompt != "" {
			worker.ReplayPrompt = worker.LastPrompt
			worker.Status = WorkerReadyForPrompt
			recoveryArmed = true
			pushEvent(&worker, EventPromptReplayArmed, WorkerReadyForPrompt, "prompt replay armed after prompt misdelivery", &WorkerEventPayload{
				Type:           "prompt_delivery",
				PromptPreview:  preview,
				ObservedTarget: observation.target,
				ObservedCWD:    observation.observedCWD,
				RecoveryArmed:  &recoveryArmed,
			})
		} else {
			worker.Status = WorkerFailed
		}
		worker.UpdatedAt = time.Now().Unix()
		r.workers[workerID] = worker
		return worker, nil
	}
	if detectRunningCue(lowered) && worker.PromptInFlight {
		worker.PromptInFlight = false
		worker.Status = WorkerRunning
		worker.LastError = nil
	}
	if detectReadyForPrompt(screenText, lowered) && worker.Status != WorkerReadyForPrompt {
		worker.Status = WorkerReadyForPrompt
		worker.PromptInFlight = false
		if worker.LastError != nil && worker.LastError.Kind == WorkerFailureTrustGate {
			worker.LastError = nil
		}
		pushEvent(&worker, EventReadyForPrompt, WorkerReadyForPrompt, "worker is ready for prompt delivery", nil)
	}
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, nil
}

func (r *Registry) ResolveTrust(workerID string) (Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return Worker{}, fmt.Errorf("worker not found: %s", workerID)
	}
	if worker.Status != WorkerTrustRequired {
		return Worker{}, fmt.Errorf("worker %s is not waiting on trust; current status: %s", workerID, worker.Status)
	}
	worker.TrustGateCleared = true
	worker.LastError = nil
	worker.Status = WorkerSpawning
	resolution := TrustResolutionManualApproval
	pushEvent(&worker, EventTrustResolved, WorkerSpawning, "trust prompt resolved manually", &WorkerEventPayload{
		Type:       "trust_prompt",
		CWD:        worker.CWD,
		Resolution: &resolution,
	})
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, nil
}

func (r *Registry) SendPrompt(workerID, prompt string) (Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return Worker{}, fmt.Errorf("worker not found: %s", workerID)
	}
	if worker.Status != WorkerReadyForPrompt {
		return Worker{}, fmt.Errorf("worker %s is not ready for prompt delivery; current status: %s", workerID, worker.Status)
	}
	nextPrompt := strings.TrimSpace(prompt)
	if nextPrompt == "" {
		nextPrompt = strings.TrimSpace(worker.ReplayPrompt)
	}
	if nextPrompt == "" {
		return Worker{}, fmt.Errorf("worker %s has no prompt to send or replay", workerID)
	}
	worker.PromptDeliveryAttempts++
	worker.PromptInFlight = true
	worker.LastPrompt = nextPrompt
	worker.ReplayPrompt = ""
	worker.LastError = nil
	worker.Status = WorkerRunning
	pushEvent(&worker, EventRunning, WorkerRunning, "prompt dispatched to worker: "+promptPreview(nextPrompt), nil)
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, nil
}

func (r *Registry) MarkPromptSent(workerID, prompt string) (Worker, error) {
	return r.SendPrompt(workerID, prompt)
}

func (r *Registry) AwaitReady(workerID string) (WorkerReadySnapshot, error) {
	worker, ok := r.Get(workerID)
	if !ok {
		return WorkerReadySnapshot{}, fmt.Errorf("worker not found: %s", workerID)
	}
	return WorkerReadySnapshot{
		WorkerID:          worker.WorkerID,
		Status:            worker.Status,
		Ready:             worker.Status == WorkerReadyForPrompt,
		Blocked:           worker.Status == WorkerTrustRequired || worker.Status == WorkerFailed,
		ReplayPromptReady: strings.TrimSpace(worker.ReplayPrompt) != "",
		LastError:         worker.LastError,
	}, nil
}

func (r *Registry) Restart(workerID string) (Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return Worker{}, fmt.Errorf("worker not found: %s", workerID)
	}
	worker.Status = WorkerSpawning
	worker.TrustGateCleared = false
	worker.LastPrompt = ""
	worker.ReplayPrompt = ""
	worker.LastError = nil
	worker.PromptDeliveryAttempts = 0
	worker.PromptInFlight = false
	pushEvent(&worker, EventRestarted, WorkerSpawning, "worker restarted", nil)
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, nil
}

func (r *Registry) Terminate(workerID string) (Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return Worker{}, fmt.Errorf("worker not found: %s", workerID)
	}
	worker.Status = WorkerFinished
	worker.PromptInFlight = false
	pushEvent(&worker, EventFinished, WorkerFinished, "worker terminated by control plane", nil)
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, nil
}

func (r *Registry) MarkFinished(workerID string) (Worker, error) {
	return r.Terminate(workerID)
}

func (r *Registry) ObserveCompletion(workerID, finishReason string, tokensOutput int) (Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return Worker{}, fmt.Errorf("worker not found: %s", workerID)
	}
	providerFailure := (finishReason == "unknown" && tokensOutput == 0) || finishReason == "error"
	switch {
	case providerFailure:
		message := fmt.Sprintf("session failed with finish=%q and output=%d", finishReason, tokensOutput)
		if finishReason == "unknown" && tokensOutput == 0 {
			message = "session completed with finish='unknown' and zero output - provider degraded or context exhausted"
		}
		worker.LastError = &WorkerFailure{
			Kind:      WorkerFailureProvider,
			Message:   message,
			CreatedAt: time.Now().Unix(),
		}
		worker.Status = WorkerFailed
		worker.PromptInFlight = false
		pushEvent(&worker, EventFailed, WorkerFailed, "provider failure classified", nil)
	case finishReason == "tool_use" || finishReason == "pause_turn" || finishReason == "max_tokens":
		worker.Status = WorkerRunning
		worker.PromptInFlight = false
		worker.LastError = nil
	default:
		worker.Status = WorkerFinished
		worker.PromptInFlight = false
		worker.LastError = nil
		pushEvent(&worker, EventFinished, WorkerFinished, "worker session completed", nil)
	}
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, nil
}

func (r *Registry) ApplyRecovery(workerID string, scenario recovery.FailureScenario) (Worker, recovery.Result, error) {
	return r.ApplyRecoveryWithExecutor(workerID, scenario, nil)
}

func (r *Registry) ApplyRecoveryWithExecutor(workerID string, scenario recovery.FailureScenario, executor recovery.StepExecutor) (Worker, recovery.Result, error) {
	r.mu.Lock()
	worker, ok := r.workers[workerID]
	if !ok {
		r.mu.Unlock()
		return Worker{}, recovery.Result{}, fmt.Errorf("worker not found: %s", workerID)
	}
	ctx := recovery.NewContext()
	for key, attempts := range worker.RecoveryAttempts {
		ctx.Attempts[key] = attempts
	}
	ctx.Events = append(ctx.Events, worker.RecoveryEvents...)
	r.mu.Unlock()

	result := recovery.Attempt(&ctx, scenario, executor)

	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok = r.workers[workerID]
	if !ok {
		return Worker{}, recovery.Result{}, fmt.Errorf("worker not found: %s", workerID)
	}
	worker.RecoveryAttempts = ctx.Attempts
	worker.RecoveryEvents = append([]recovery.Event(nil), ctx.Events...)
	switch result.Kind {
	case recovery.ResultRecovered:
		pushEvent(&worker, EventRunning, worker.Status, "recovery recovered: "+string(scenario), nil)
	case recovery.ResultPartialRecovery:
		pushEvent(&worker, EventFailed, worker.Status, "recovery partial: "+string(scenario), nil)
	default:
		pushEvent(&worker, EventFailed, worker.Status, "recovery escalated: "+string(scenario), nil)
	}
	worker.UpdatedAt = time.Now().Unix()
	r.workers[workerID] = worker
	return worker, result, nil
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	workers := make([]Worker, 0, len(r.workers))
	for _, worker := range r.workers {
		workers = append(workers, worker)
	}
	return Snapshot{Workers: workers}
}

func (r *Registry) Replace(snapshot Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers = map[string]Worker{}
	for _, worker := range snapshot.Workers {
		r.workers[worker.WorkerID] = worker
	}
}

func StatePath(root string) string {
	return filepath.Join(root, ".ascaris", "worker-state.json")
}

func Load(root string) (Snapshot, error) {
	data, err := os.ReadFile(StatePath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return Snapshot{}, nil
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func Save(root string, snapshot Snapshot) error {
	path := StatePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func RenderText(snapshot Snapshot) string {
	if len(snapshot.Workers) == 0 {
		return "Workers\n  No worker state recorded."
	}
	lines := []string{"Workers"}
	for _, worker := range snapshot.Workers {
		line := "  " + worker.WorkerID + " · " + string(worker.Status) + " · " + worker.CWD
		if worker.LastError != nil {
			line += " · " + worker.LastError.Message
		}
		if len(worker.RecoveryEvents) > 0 {
			last := worker.RecoveryEvents[len(worker.RecoveryEvents)-1]
			line += " · recovery=" + string(last.Scenario) + ":" + string(last.Result.Kind)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func RenderJSON(snapshot Snapshot) string {
	data, _ := json.MarshalIndent(snapshot, "", "  ")
	return string(data)
}

func detectTrustPrompt(screen string) bool {
	return strings.Contains(screen, "trust this workspace") || strings.Contains(screen, "trust and continue")
}

func detectReadyForPrompt(screenText, lowered string) bool {
	return strings.Contains(lowered, "ready for prompt") || strings.Contains(lowered, "ascaris>") || strings.Contains(lowered, "prompt>")
}

func detectRunningCue(screen string) bool {
	return strings.Contains(screen, "running") || strings.Contains(screen, "responding") || strings.Contains(screen, "working")
}

type promptObservation struct {
	target      WorkerPromptTarget
	observedCWD *string
	detail      string
}

func detectPromptMisdelivery(screenText, lowered, lastPrompt, cwd string) *promptObservation {
	switch {
	case strings.Contains(lowered, "command not found"):
		return &promptObservation{target: PromptTargetShell, detail: "worker prompt landed in shell instead of coding agent"}
	case strings.Contains(lowered, "wrong pane"), strings.Contains(lowered, "unexpected target"):
		cleaned := strings.TrimSpace(cwd)
		return &promptObservation{target: PromptTargetWrongTarget, observedCWD: &cleaned, detail: "worker prompt landed in the wrong target"}
	case strings.TrimSpace(lastPrompt) != "" && strings.Contains(screenText, lastPrompt) && !strings.Contains(screenText, cwd):
		return &promptObservation{target: PromptTargetUnknown, detail: "worker prompt delivery failed before reaching coding agent"}
	default:
		return nil
	}
}

func matchesTrustedRoot(cwd string, trustedRoots []string) bool {
	for _, root := range trustedRoots {
		root = filepath.Clean(root)
		if root == "." {
			continue
		}
		if strings.HasPrefix(filepath.Clean(cwd), root) {
			return true
		}
	}
	return false
}

func pushEvent(worker *Worker, kind WorkerEventKind, status WorkerStatus, detail string, payload *WorkerEventPayload) {
	worker.Events = append(worker.Events, WorkerEvent{
		Seq:       uint64(len(worker.Events) + 1),
		Kind:      kind,
		Status:    status,
		Detail:    detail,
		Payload:   payload,
		Timestamp: time.Now().Unix(),
	})
}

func promptPreview(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if len(trimmed) <= 64 {
		return trimmed
	}
	return trimmed[:61] + "..."
}

func promptMisdeliveryMessage(observation promptObservation, preview, cwd string) string {
	switch observation.target {
	case PromptTargetShell:
		return "worker prompt landed in shell instead of coding agent: " + preview
	case PromptTargetWrongTarget:
		return "worker prompt landed in the wrong target instead of " + cwd + ": " + preview
	default:
		return "worker prompt delivery failed before reaching coding agent: " + preview
	}
}

func Itoa(v int) string {
	return strconv.Itoa(v)
}
