package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type TeamStatus string

const (
	TeamStatusCreated   TeamStatus = "created"
	TeamStatusRunning   TeamStatus = "running"
	TeamStatusCompleted TeamStatus = "completed"
	TeamStatusDeleted   TeamStatus = "deleted"
)

type Team struct {
	TeamID    string     `json:"team_id"`
	Name      string     `json:"name"`
	TaskIDs   []string   `json:"task_ids"`
	Status    TeamStatus `json:"status"`
	CreatedAt int64      `json:"created_at"`
	UpdatedAt int64      `json:"updated_at"`
}

type TeamSnapshot struct {
	Teams []Team `json:"teams"`
}

type TeamRegistry struct {
	mu      sync.Mutex
	counter uint64
	teams   map[string]Team
}

func NewTeamRegistry() *TeamRegistry {
	return &TeamRegistry{teams: map[string]Team{}}
}

func LoadTeamRegistry(root string) (*TeamRegistry, error) {
	registry := NewTeamRegistry()
	snapshot, err := LoadTeams(root)
	if err != nil {
		return nil, err
	}
	registry.Replace(snapshot)
	return registry, nil
}

func SaveTeamRegistry(root string, registry *TeamRegistry) error {
	if registry == nil {
		return nil
	}
	return SaveTeams(root, registry.Snapshot())
}

func (r *TeamRegistry) Create(name string, taskIDs []string) Team {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	now := time.Now().Unix()
	teamID := fmt.Sprintf("team_%08x_%d", now, r.counter)
	team := Team{
		TeamID:    teamID,
		Name:      name,
		TaskIDs:   append([]string(nil), taskIDs...),
		Status:    TeamStatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.teams[teamID] = team
	return team
}

func (r *TeamRegistry) Get(teamID string) (Team, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	team, ok := r.teams[teamID]
	return team, ok
}

func (r *TeamRegistry) List() []Team {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Team, 0, len(r.teams))
	for _, team := range r.teams {
		items = append(items, team)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].TeamID < items[j].TeamID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items
}

func (r *TeamRegistry) Delete(teamID string) (Team, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	team, ok := r.teams[teamID]
	if !ok {
		return Team{}, fmt.Errorf("team not found: %s", teamID)
	}
	team.Status = TeamStatusDeleted
	team.UpdatedAt = time.Now().Unix()
	r.teams[teamID] = team
	return team, nil
}

func (r *TeamRegistry) Snapshot() TeamSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Team, 0, len(r.teams))
	for _, team := range r.teams {
		items = append(items, team)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].TeamID < items[j].TeamID
		}
		return items[i].CreatedAt < items[j].CreatedAt
	})
	return TeamSnapshot{Teams: items}
}

func (r *TeamRegistry) Replace(snapshot TeamSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.teams = map[string]Team{}
	r.counter = 0
	for _, team := range snapshot.Teams {
		r.teams[team.TeamID] = team
		r.counter++
	}
}

type CronEntry struct {
	CronID      string  `json:"cron_id"`
	Schedule    string  `json:"schedule"`
	Prompt      string  `json:"prompt"`
	Description *string `json:"description,omitempty"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
	LastRunAt   *int64  `json:"last_run_at,omitempty"`
	RunCount    uint64  `json:"run_count"`
}

type CronSnapshot struct {
	Entries []CronEntry `json:"entries"`
}

type CronRegistry struct {
	mu      sync.Mutex
	counter uint64
	entries map[string]CronEntry
}

func NewCronRegistry() *CronRegistry {
	return &CronRegistry{entries: map[string]CronEntry{}}
}

func LoadCronRegistry(root string) (*CronRegistry, error) {
	registry := NewCronRegistry()
	snapshot, err := LoadCrons(root)
	if err != nil {
		return nil, err
	}
	registry.Replace(snapshot)
	return registry, nil
}

func SaveCronRegistry(root string, registry *CronRegistry) error {
	if registry == nil {
		return nil
	}
	return SaveCrons(root, registry.Snapshot())
}

func (r *CronRegistry) Create(schedule, prompt string, description *string) CronEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counter++
	now := time.Now().Unix()
	cronID := fmt.Sprintf("cron_%08x_%d", now, r.counter)
	entry := CronEntry{
		CronID:      cronID,
		Schedule:    schedule,
		Prompt:      prompt,
		Description: cloneOptionalString(description),
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	r.entries[cronID] = entry
	return entry
}

func (r *CronRegistry) Get(cronID string) (CronEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[cronID]
	return entry, ok
}

func (r *CronRegistry) List(enabledOnly bool) []CronEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]CronEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		if enabledOnly && !entry.Enabled {
			continue
		}
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].CronID < items[j].CronID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items
}

func (r *CronRegistry) Delete(cronID string) (CronEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[cronID]
	if !ok {
		return CronEntry{}, fmt.Errorf("cron not found: %s", cronID)
	}
	delete(r.entries, cronID)
	return entry, nil
}

func (r *CronRegistry) Snapshot() CronSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]CronEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		items = append(items, entry)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt == items[j].CreatedAt {
			return items[i].CronID < items[j].CronID
		}
		return items[i].CreatedAt < items[j].CreatedAt
	})
	return CronSnapshot{Entries: items}
}

func (r *CronRegistry) Replace(snapshot CronSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = map[string]CronEntry{}
	r.counter = 0
	for _, entry := range snapshot.Entries {
		r.entries[entry.CronID] = entry
		r.counter++
	}
}

func TeamStatePath(root string) string {
	return filepath.Join(root, ".ascaris", "teams.json")
}

func CronStatePath(root string) string {
	return filepath.Join(root, ".ascaris", "crons.json")
}

func LoadTeams(root string) (TeamSnapshot, error) {
	return loadSnapshot[TeamSnapshot](TeamStatePath(root))
}

func SaveTeams(root string, snapshot TeamSnapshot) error {
	return saveSnapshot(TeamStatePath(root), snapshot)
}

func LoadCrons(root string) (CronSnapshot, error) {
	return loadSnapshot[CronSnapshot](CronStatePath(root))
}

func SaveCrons(root string, snapshot CronSnapshot) error {
	return saveSnapshot(CronStatePath(root), snapshot)
}

func loadSnapshot[T any](path string) (T, error) {
	var snapshot T
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, nil
		}
		return snapshot, err
	}
	if len(data) == 0 {
		return snapshot, nil
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func saveSnapshot(path string, snapshot any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
