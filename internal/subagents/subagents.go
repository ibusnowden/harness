package subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ascaris/internal/api"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Assignment struct {
	AssignmentID       string   `json:"assignment_id"`
	WorkerID           string   `json:"worker_id"`
	Role               string   `json:"role"`
	Prompt             string   `json:"prompt"`
	Context            string   `json:"context,omitempty"`
	AllowedTools       []string `json:"allowed_tools,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Status             Status   `json:"status"`
	CreatedAtMS        int64    `json:"created_at_ms"`
	UpdatedAtMS        int64    `json:"updated_at_ms"`
	StartedAtMS        int64    `json:"started_at_ms,omitempty"`
	FinishedAtMS       int64    `json:"finished_at_ms,omitempty"`
	ResultSummary      string   `json:"result_summary,omitempty"`
	Error              string   `json:"error,omitempty"`
	InspectedFiles     []string `json:"inspected_files,omitempty"`
	ChangedFiles       []string `json:"changed_files,omitempty"`
	Verification       string   `json:"verification,omitempty"`
	Usage              api.Usage `json:"usage"`
}

type Snapshot struct {
	Assignments []Assignment `json:"assignments"`
}

func (r *Registry) MarkRunning(id string) (Assignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	assignment, ok := r.assignments[id]
	if !ok {
		return Assignment{}, fmt.Errorf("subagent assignment not found: %s", id)
	}
	now := time.Now().UnixMilli()
	assignment.Status = StatusRunning
	assignment.StartedAtMS = now
	assignment.UpdatedAtMS = now
	r.assignments[id] = assignment
	return assignment, nil
}

func (r *Registry) Complete(id string, result Result) (Assignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	assignment, ok := r.assignments[id]
	if !ok {
		return Assignment{}, fmt.Errorf("subagent assignment not found: %s", id)
	}
	now := time.Now().UnixMilli()
	assignment.Status = StatusCompleted
	assignment.FinishedAtMS = now
	assignment.UpdatedAtMS = now
	assignment.ResultSummary = strings.TrimSpace(result.Summary)
	assignment.InspectedFiles = cleanList(result.InspectedFiles)
	assignment.ChangedFiles = cleanList(result.ChangedFiles)
	assignment.Verification = strings.TrimSpace(result.Verification)
	assignment.Usage = result.Usage
	assignment.Error = ""
	r.assignments[id] = assignment
	return assignment, nil
}

func (r *Registry) Fail(id string, err error, usage api.Usage) (Assignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	assignment, ok := r.assignments[id]
	if !ok {
		return Assignment{}, fmt.Errorf("subagent assignment not found: %s", id)
	}
	now := time.Now().UnixMilli()
	assignment.Status = StatusFailed
	assignment.FinishedAtMS = now
	assignment.UpdatedAtMS = now
	assignment.Usage = usage
	if err != nil {
		assignment.Error = err.Error()
	}
	r.assignments[id] = assignment
	return assignment, nil
}

type Result struct {
	Summary        string
	InspectedFiles []string
	ChangedFiles   []string
	Verification   string
	Usage          api.Usage
}

type Registry struct {
	mu          sync.Mutex
	counter     uint64
	assignments map[string]Assignment
}

func NewRegistry() *Registry {
	return &Registry{assignments: map[string]Assignment{}}
}

func LoadRegistry(root string) (*Registry, error) {
	registry := NewRegistry()
	snapshot, err := Load(root)
	if err != nil {
		return nil, err
	}
	registry.Replace(snapshot)
	return registry, nil
}

func SaveRegistry(root string, registry *Registry) error {
	if registry == nil {
		return nil
	}
	return Save(root, registry.Snapshot())
}

func (r *Registry) Create(workerID, role, prompt, context string, allowedTools, acceptanceCriteria []string) (Assignment, error) {
	if strings.TrimSpace(prompt) == "" {
		return Assignment{}, fmt.Errorf("delegate_task prompt is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	now := time.Now().UnixMilli()
	id := fmt.Sprintf("subagent_%d_%d", now, r.counter)
	assignment := Assignment{
		AssignmentID:       id,
		WorkerID:           strings.TrimSpace(workerID),
		Role:               fallback(strings.TrimSpace(role), "worker"),
		Prompt:             strings.TrimSpace(prompt),
		Context:            strings.TrimSpace(context),
		AllowedTools:       cleanList(allowedTools),
		AcceptanceCriteria: cleanList(acceptanceCriteria),
		Status:             StatusPending,
		CreatedAtMS:        now,
		UpdatedAtMS:        now,
	}
	r.assignments[id] = assignment
	return assignment, nil
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Assignment, 0, len(r.assignments))
	for _, assignment := range r.assignments {
		items = append(items, assignment)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAtMS == items[j].CreatedAtMS {
			return items[i].AssignmentID < items[j].AssignmentID
		}
		return items[i].CreatedAtMS < items[j].CreatedAtMS
	})
	return Snapshot{Assignments: items}
}

func (r *Registry) Replace(snapshot Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assignments = map[string]Assignment{}
	r.counter = 0
	for _, assignment := range snapshot.Assignments {
		r.assignments[assignment.AssignmentID] = assignment
		r.counter++
	}
}

func Load(root string) (Snapshot, error) {
	data, err := os.ReadFile(path(root))
	if os.IsNotExist(err) {
		return Snapshot{}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func Save(root string, snapshot Snapshot) error {
	target := path(root)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

func path(root string) string {
	return filepath.Join(root, ".ascaris", "subagents.json")
}

func cleanList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}
