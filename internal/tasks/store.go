package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

type Task struct {
	ID                 int      `json:"id"`
	Title              string   `json:"title"`
	Goal               string   `json:"goal,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	AllowedTools       []string `json:"allowed_tools,omitempty"`
	Status             string   `json:"status"`
	BlockedBy          []int    `json:"blocked_by,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	UpdatedMS          int64    `json:"updated_at_ms"`
}

type TaskList struct {
	Tasks   []Task `json:"tasks"`
	Version int    `json:"version"`
}

var mu sync.Mutex

func tasksPath(root string) string {
	return filepath.Join(root, ".ascaris", "tasks.json")
}

func Load(root string) (TaskList, error) {
	mu.Lock()
	defer mu.Unlock()
	return load(root)
}

func load(root string) (TaskList, error) {
	data, err := os.ReadFile(tasksPath(root))
	if os.IsNotExist(err) {
		return TaskList{}, nil
	}
	if err != nil {
		return TaskList{}, err
	}
	var tl TaskList
	if err := json.Unmarshal(data, &tl); err != nil {
		return TaskList{}, err
	}
	return tl, nil
}

func save(root string, tl TaskList) error {
	path := tasksPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tl.Version++
	data, err := json.MarshalIndent(tl, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write via temp file + rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Create(root, title, goal string, acceptanceCriteria, allowedTools []string, blockedBy []int) (Task, error) {
	mu.Lock()
	defer mu.Unlock()
	tl, err := load(root)
	if err != nil {
		return Task{}, err
	}
	t := Task{
		ID:                 len(tl.Tasks) + 1,
		Title:              title,
		Goal:               goal,
		AcceptanceCriteria: cleanStrings(acceptanceCriteria),
		AllowedTools:       cleanStrings(allowedTools),
		Status:             StatusOpen,
		BlockedBy:          blockedBy,
		UpdatedMS:          time.Now().UnixMilli(),
	}
	tl.Tasks = append(tl.Tasks, t)
	if err := save(root, tl); err != nil {
		return Task{}, err
	}
	return t, nil
}

func Replace(root string, items []Task) error {
	mu.Lock()
	defer mu.Unlock()
	tl := TaskList{
		Tasks:   make([]Task, 0, len(items)),
		Version: 0,
	}
	now := time.Now().UnixMilli()
	for index, item := range items {
		item.ID = index + 1
		item.Title = strings.TrimSpace(item.Title)
		item.Goal = strings.TrimSpace(item.Goal)
		item.AcceptanceCriteria = cleanStrings(item.AcceptanceCriteria)
		item.AllowedTools = cleanStrings(item.AllowedTools)
		item.Status = strings.TrimSpace(item.Status)
		if item.Status == "" {
			item.Status = StatusOpen
		}
		item.Notes = strings.TrimSpace(item.Notes)
		item.UpdatedMS = now
		tl.Tasks = append(tl.Tasks, item)
	}
	return save(root, tl)
}

func Update(root string, id int, status string) (Task, error) {
	return UpdateWithNotes(root, id, status, "")
}

func UpdateWithNotes(root string, id int, status, notes string) (Task, error) {
	switch status {
	case StatusOpen, StatusInProgress, StatusDone, StatusCancelled:
	default:
		return Task{}, fmt.Errorf("invalid status %q: must be open, in_progress, done, or cancelled", status)
	}
	mu.Lock()
	defer mu.Unlock()
	tl, err := load(root)
	if err != nil {
		return Task{}, err
	}
	for i, t := range tl.Tasks {
		if t.ID == id {
			tl.Tasks[i].Status = status
			tl.Tasks[i].Notes = strings.TrimSpace(notes)
			tl.Tasks[i].UpdatedMS = time.Now().UnixMilli()
			if err := save(root, tl); err != nil {
				return Task{}, err
			}
			return tl.Tasks[i], nil
		}
	}
	return Task{}, fmt.Errorf("task #%d not found", id)
}

// ModTime returns the modification time of the tasks file, used for
// cross-session change detection without a file watcher dependency.
func ModTime(root string) time.Time {
	info, err := os.Stat(tasksPath(root))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// IsBlocked reports whether t has unmet dependencies in the given list.
func IsBlocked(t Task, all []Task) bool {
	if len(t.BlockedBy) == 0 {
		return false
	}
	done := make(map[int]bool, len(all))
	for _, other := range all {
		if other.Status == StatusDone || other.Status == StatusCancelled {
			done[other.ID] = true
		}
	}
	for _, dep := range t.BlockedBy {
		if !done[dep] {
			return true
		}
	}
	return false
}

func NextOpenUnblocked(all []Task) (Task, bool) {
	for _, task := range all {
		if task.Status != StatusOpen {
			continue
		}
		if IsBlocked(task, all) {
			continue
		}
		return task, true
	}
	return Task{}, false
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
