package sessions

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ascaris/internal/api"
	"ascaris/internal/config"
)

const (
	primarySessionExtension = ".jsonl"
)

var latestAliases = map[string]struct{}{
	"":       {},
	"latest": {},
	"last":   {},
	"recent": {},
}

type StoredSession struct {
	SessionID    string   `json:"session_id"`
	Messages     []string `json:"messages"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
}

type ManagedSession struct {
	Meta     SessionMeta        `json:"meta"`
	Messages []api.InputMessage `json:"messages"`
	Path     string             `json:"-"`
}

type SessionMeta struct {
	Version       int             `json:"version"`
	SessionID     string          `json:"session_id"`
	CreatedAtMS   int64           `json:"created_at_ms"`
	UpdatedAtMS   int64           `json:"updated_at_ms"`
	Model         string          `json:"model,omitempty"`
	Usage         api.Usage       `json:"usage"`
	WorkspaceRoot string          `json:"workspace_root,omitempty"`
	Fork          *SessionFork    `json:"fork,omitempty"`
	Compaction    *SessionCompact `json:"compaction,omitempty"`
	PromptHistory []SessionPrompt `json:"prompt_history,omitempty"`
}

type SessionFork struct {
	ParentSessionID string `json:"parent_session_id"`
	BranchName      string `json:"branch_name,omitempty"`
}

type SessionCompact struct {
	Count               int    `json:"count"`
	RemovedMessageCount int    `json:"removed_message_count"`
	Summary             string `json:"summary"`
}

type SessionPrompt struct {
	TimestampMS int64  `json:"timestamp_ms"`
	Text        string `json:"text"`
}

type ManagedSessionSummary struct {
	ID                  string `json:"id"`
	Path                string `json:"path"`
	ModifiedEpochMillis int64  `json:"modified_epoch_millis"`
	MessageCount        int    `json:"message_count"`
	ParentSessionID     string `json:"parent_session_id,omitempty"`
	BranchName          string `json:"branch_name,omitempty"`
}

func NewManagedSession(sessionID, model string) ManagedSession {
	now := time.Now().UnixMilli()
	return ManagedSession{
		Meta: SessionMeta{
			Version:     4,
			SessionID:   sessionID,
			CreatedAtMS: now,
			UpdatedAtMS: now,
			Model:       model,
		},
	}
}

func ConfigHome(root string) string {
	return config.ConfigHome(root)
}

func SessionRoot(root string) string {
	return filepath.Join(ConfigHome(root), "sessions")
}

func SessionDir(root string) string {
	return filepath.Join(SessionRoot(root), workspaceFingerprint(root))
}

func LegacySessionDir(root string) string {
	return SessionRoot(root)
}

func Save(session StoredSession, root string) (string, error) {
	managed := NewManagedSession(session.SessionID, "")
	managed.Meta.Usage = api.Usage{
		InputTokens:  session.InputTokens,
		OutputTokens: session.OutputTokens,
	}
	for _, message := range session.Messages {
		managed.Messages = append(managed.Messages, api.UserTextMessage(message))
	}
	return SaveManaged(managed, root)
}

func SaveManaged(session ManagedSession, root string) (string, error) {
	if strings.TrimSpace(session.Meta.SessionID) == "" {
		return "", errors.New("session id is required")
	}
	now := time.Now().UnixMilli()
	if session.Meta.Version == 0 {
		session.Meta.Version = 4
	}
	if session.Meta.CreatedAtMS == 0 {
		session.Meta.CreatedAtMS = now
	}
	session.Meta.UpdatedAtMS = now
	if strings.TrimSpace(session.Meta.WorkspaceRoot) == "" {
		session.Meta.WorkspaceRoot = filepath.Clean(root)
	}
	dir := SessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := managedPath(dir, session.Meta.SessionID, primarySessionExtension)
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		Type string      `json:"type"`
		Meta SessionMeta `json:"meta"`
	}{
		Type: "session_meta",
		Meta: session.Meta,
	}); err != nil {
		return "", err
	}
	for _, message := range session.Messages {
		if err := encoder.Encode(struct {
			Type    string           `json:"type"`
			Message api.InputMessage `json:"message"`
		}{
			Type:    "message",
			Message: message,
		}); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(latestAliasPath(root), []byte(session.Meta.SessionID), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func Load(sessionID, root string) (StoredSession, error) {
	managed, err := LoadManaged(root, sessionID)
	if err != nil {
		return StoredSession{}, err
	}
	return managed.Stored(), nil
}

func LoadManaged(root, reference string) (ManagedSession, error) {
	path, err := resolveReference(root, reference)
	if err != nil {
		return ManagedSession{}, err
	}
	session, err := loadManagedFromPath(path)
	if err != nil {
		return ManagedSession{}, err
	}
	session.Path = path
	if needsMigration(path, root) {
		if migratedPath, saveErr := SaveManaged(session, root); saveErr == nil {
			session.Path = migratedPath
		}
	}
	return session, nil
}

func List(root string) ([]ManagedSessionSummary, error) {
	type candidate struct {
		summary ManagedSessionSummary
	}
	merged := map[string]ManagedSessionSummary{}
	for _, dir := range []string{SessionDir(root), LegacySessionDir(root)} {
		summaries, err := listDir(dir)
		if err != nil {
			return nil, err
		}
		for _, summary := range summaries {
			current, ok := merged[summary.ID]
			if !ok || summary.ModifiedEpochMillis > current.ModifiedEpochMillis {
				merged[summary.ID] = summary
			}
		}
	}
	items := make([]ManagedSessionSummary, 0, len(merged))
	for _, summary := range merged {
		items = append(items, summary)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ModifiedEpochMillis == items[j].ModifiedEpochMillis {
			return items[i].ID > items[j].ID
		}
		return items[i].ModifiedEpochMillis > items[j].ModifiedEpochMillis
	})
	return items, nil
}

func Latest(root string) (ManagedSessionSummary, error) {
	items, err := List(root)
	if err != nil {
		return ManagedSessionSummary{}, err
	}
	if len(items) == 0 {
		return ManagedSessionSummary{}, os.ErrNotExist
	}
	return items[0], nil
}

func Delete(root, reference string) error {
	path, err := resolveReference(root, reference)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return refreshLatest(root)
}

func Export(root, reference, target string) (string, error) {
	session, err := LoadManaged(root, reference)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(target) == "" {
		target = filepath.Join(ConfigHome(root), "exports", session.Meta.SessionID+".json")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

func Fork(root, reference, branchName string) (ManagedSession, error) {
	parent, err := LoadManaged(root, reference)
	if err != nil {
		return ManagedSession{}, err
	}
	child := NewManagedSession(newSessionID(), parent.Meta.Model)
	child.Messages = append([]api.InputMessage(nil), parent.Messages...)
	child.Meta.Usage = parent.Meta.Usage
	child.Meta.WorkspaceRoot = parent.Meta.WorkspaceRoot
	child.Meta.Compaction = parent.Meta.Compaction
	child.Meta.PromptHistory = append([]SessionPrompt(nil), parent.Meta.PromptHistory...)
	child.Meta.Fork = &SessionFork{
		ParentSessionID: parent.Meta.SessionID,
		BranchName:      strings.TrimSpace(branchName),
	}
	path, err := SaveManaged(child, root)
	if err != nil {
		return ManagedSession{}, err
	}
	child.Path = path
	return child, nil
}

func Switch(root, reference string) (ManagedSessionSummary, error) {
	path, err := resolveReference(root, reference)
	if err != nil {
		return ManagedSessionSummary{}, err
	}
	summary, err := summaryFromPath(path)
	if err != nil {
		return ManagedSessionSummary{}, err
	}
	if err := os.MkdirAll(filepath.Dir(latestAliasPath(root)), 0o755); err != nil {
		return ManagedSessionSummary{}, err
	}
	if err := os.WriteFile(latestAliasPath(root), []byte(summary.ID), 0o644); err != nil {
		return ManagedSessionSummary{}, err
	}
	return summary, nil
}

func Clear(root string) error {
	if err := os.Remove(latestAliasPath(root)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *ManagedSession) RecordPrompt(text string) {
	s.Meta.PromptHistory = append(s.Meta.PromptHistory, SessionPrompt{
		TimestampMS: time.Now().UnixMilli(),
		Text:        text,
	})
	s.touch()
}

func (s *ManagedSession) RecordCompaction(summary string, removed int) {
	count := 1
	if s.Meta.Compaction != nil {
		count = s.Meta.Compaction.Count + 1
	}
	s.Meta.Compaction = &SessionCompact{
		Count:               count,
		RemovedMessageCount: removed,
		Summary:             summary,
	}
	s.touch()
}

func (s ManagedSession) Stored() StoredSession {
	return StoredSession{
		SessionID:    s.Meta.SessionID,
		Messages:     flattenMessages(s.Messages),
		InputTokens:  s.Meta.Usage.InputTokens + s.Meta.Usage.CacheCreationInputTokens + s.Meta.Usage.CacheReadInputTokens,
		OutputTokens: s.Meta.Usage.OutputTokens,
	}
}

func (s *ManagedSession) touch() {
	s.Meta.UpdatedAtMS = time.Now().UnixMilli()
}

func listDir(dir string) ([]ManagedSessionSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	summaries := make([]ManagedSessionSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, primarySessionExtension) {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		summary, err := summaryFromPath(path)
		if err != nil {
			summary = ManagedSessionSummary{
				ID:                  strings.TrimSuffix(name, primarySessionExtension),
				Path:                path,
				ModifiedEpochMillis: info.ModTime().UnixMilli(),
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func summaryFromPath(path string) (ManagedSessionSummary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ManagedSessionSummary{}, err
	}
	session, err := loadManagedFromPath(path)
	if err != nil {
		return ManagedSessionSummary{}, err
	}
	summary := ManagedSessionSummary{
		ID:                  session.Meta.SessionID,
		Path:                path,
		ModifiedEpochMillis: info.ModTime().UnixMilli(),
		MessageCount:        len(session.Messages),
	}
	if session.Meta.Fork != nil {
		summary.ParentSessionID = session.Meta.Fork.ParentSessionID
		summary.BranchName = session.Meta.Fork.BranchName
	}
	return summary, nil
}

func resolveReference(root, reference string) (string, error) {
	trimmed := strings.TrimSpace(reference)
	if _, ok := latestAliases[strings.ToLower(trimmed)]; ok {
		if id, err := readLatestAlias(root); err == nil && id != "" {
			if path, ok := findSessionPath(root, id); ok {
				return path, nil
			}
		}
		latest, err := Latest(root)
		if err != nil {
			return "", err
		}
		return latest.Path, nil
	}
	if direct, ok := resolvePathReference(root, trimmed); ok {
		return direct, nil
	}
	if path, ok := findSessionPath(root, trimmed); ok {
		return path, nil
	}
	return "", fmt.Errorf("managed session %q was not found", reference)
}

func readLatestAlias(root string) (string, error) {
	data, err := os.ReadFile(latestAliasPath(root))
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	data, err = os.ReadFile(filepath.Join(LegacySessionDir(root), "latest"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func resolvePathReference(root, reference string) (string, bool) {
	if reference == "" {
		return "", false
	}
	direct := filepath.Clean(reference)
	if filepath.IsAbs(direct) {
		if statExists(direct) {
			return direct, true
		}
		return "", false
	}
	if strings.Contains(reference, string(filepath.Separator)) || strings.Contains(reference, ".json") {
		candidate := filepath.Join(root, reference)
		if statExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func findSessionPath(root, sessionID string) (string, bool) {
	for _, dir := range []string{SessionDir(root), LegacySessionDir(root)} {
		path := managedPath(dir, sessionID, primarySessionExtension)
		if statExists(path) {
			return path, true
		}
	}
	return "", false
}

func loadManagedFromPath(path string) (ManagedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return ManagedSession{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var session ManagedSession
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			return ManagedSession{}, err
		}
		switch header.Type {
		case "session_meta":
			var entry struct {
				Type string      `json:"type"`
				Meta SessionMeta `json:"meta"`
			}
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return ManagedSession{}, err
			}
			session.Meta = entry.Meta
		case "message":
			var entry struct {
				Type    string           `json:"type"`
				Message api.InputMessage `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return ManagedSession{}, err
			}
			session.Messages = append(session.Messages, entry.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return ManagedSession{}, err
	}
	if strings.TrimSpace(session.Meta.SessionID) == "" {
		return ManagedSession{}, fmt.Errorf("managed session %q is missing metadata", path)
	}
	session.Path = path
	return session, nil
}

func refreshLatest(root string) error {
	items, err := List(root)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		if err := os.Remove(latestAliasPath(root)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(latestAliasPath(root)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(latestAliasPath(root), []byte(items[0].ID), 0o644)
}

func latestAliasPath(root string) string {
	return filepath.Join(SessionDir(root), "latest")
}

func managedPath(dir, sessionID, ext string) string {
	return filepath.Join(dir, sessionID+ext)
}

func needsMigration(path, root string) bool {
	dir := filepath.Clean(filepath.Dir(path))
	return dir != filepath.Clean(SessionDir(root))
}

func workspaceFingerprint(root string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(filepath.Clean(root)))
	return fmt.Sprintf("%016x", hash.Sum64())
}

func newSessionID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func flattenMessages(messages []api.InputMessage) []string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		text := flattenMessage(message)
		if text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

func flattenMessage(message api.InputMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "tool_result":
			for _, content := range block.Content {
				if content.Type == "text" && content.Text != "" {
					parts = append(parts, content.Text)
				}
			}
		case "tool_use":
			if block.Name != "" {
				parts = append(parts, block.Name)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
