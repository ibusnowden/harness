package promptcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ascaris/internal/api"
	"ascaris/internal/config"
)

const (
	defaultCompletionTTL = 30 * time.Second
	maxSanitizedLength   = 80
)

type Event struct {
	Type          string `json:"type"`
	Source        string `json:"source"`
	RequestHash   string `json:"request_hash"`
	CacheKey      string `json:"cache_key"`
	SessionID     string `json:"session_id"`
	ResponseModel string `json:"response_model,omitempty"`
}

type Cache struct {
	root      string
	sessionID string
	ttl       time.Duration
}

type CompletionEntry struct {
	CachedAtUnixSecs int64               `json:"cached_at_unix_secs"`
	RequestHash      string              `json:"request_hash"`
	Response         api.MessageResponse `json:"response"`
}

func New(root, sessionID string) *Cache {
	return &Cache{
		root:      baseCacheRoot(root),
		sessionID: sessionID,
		ttl:       defaultCompletionTTL,
	}
}

func (c *Cache) Lookup(request api.MessageRequest) (api.MessageResponse, Event, bool) {
	requestHash := requestHash(request)
	path := c.completionEntryPath(requestHash)
	data, err := os.ReadFile(path)
	if err != nil {
		return api.MessageResponse{}, Event{}, false
	}
	var entry CompletionEntry
	if json.Unmarshal(data, &entry) != nil {
		return api.MessageResponse{}, Event{}, false
	}
	if time.Since(time.Unix(entry.CachedAtUnixSecs, 0)) >= c.ttl {
		_ = os.Remove(path)
		return api.MessageResponse{}, Event{}, false
	}
	return entry.Response, Event{
		Type:          "prompt_cache_hit",
		Source:        "completion-cache",
		RequestHash:   requestHash,
		CacheKey:      filepath.Base(path),
		SessionID:     c.sessionID,
		ResponseModel: entry.Response.Model,
	}, true
}

func (c *Cache) Store(request api.MessageRequest, response api.MessageResponse) (Event, error) {
	requestHash := requestHash(request)
	path := c.completionEntryPath(requestHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Event{}, err
	}
	data, err := json.Marshal(CompletionEntry{
		CachedAtUnixSecs: time.Now().Unix(),
		RequestHash:      requestHash,
		Response:         response,
	})
	if err != nil {
		return Event{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Event{}, err
	}
	return Event{
		Type:          "prompt_cache_write",
		Source:        "api-response",
		RequestHash:   requestHash,
		CacheKey:      filepath.Base(path),
		SessionID:     c.sessionID,
		ResponseModel: response.Model,
	}, nil
}

func (c *Cache) completionEntryPath(requestHash string) string {
	sessionDir := filepath.Join(c.root, sanitizePathSegment(c.sessionID), "completions")
	return filepath.Join(sessionDir, requestHash+".json")
}

func baseCacheRoot(root string) string {
	return filepath.Join(config.ConfigHome(root), "cache", "prompt-cache")
}

func requestHash(request api.MessageRequest) string {
	data, _ := json.Marshal(request)
	sum := sha256.Sum256(data)
	return "v1-" + hex.EncodeToString(sum[:])
}

var nonAlphaNumeric = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func sanitizePathSegment(value string) string {
	sanitized := nonAlphaNumeric.ReplaceAllString(value, "-")
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		sanitized = "default"
	}
	if len(sanitized) <= maxSanitizedLength {
		return sanitized
	}
	return sanitized[:maxSanitizedLength]
}
