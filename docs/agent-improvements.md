# Agent Improvement Plan

## Overview
This document outlines specific improvements to address identified weaknesses in the Ascaris coding agent.

## Identified Weaknesses & Solutions

### 1. Limited Autonomous Planning
**Current State:** No explicit long-term planning command or autonomous project decomposition.

**Proposed Solution:**
- Implement a `/plan` command that breaks down complex tasks
- Create a planning module that can:
  - Decompose large tasks into subtasks
  - Identify dependencies between tasks
  - Estimate complexity and time
  - Generate execution roadmaps

**Implementation:**
```go
// internal/planning/planner.go
package planning

type Task struct {
    ID           string
    Description  string
    Dependencies []string
    Complexity   int
    Status       string
}

type Plan struct {
    Tasks      []Task
    TotalSteps int
    Estimated  time.Duration
}

func DecomposeTask(prompt string) Plan {
    // Parse prompt and break into subtasks
    // Identify dependencies
    // Return structured plan
}
```

### 2. No Persistent Memory
**Current State:** Each session starts fresh; no memory of previous sessions.

**Proposed Solution:**
- Implement session memory storage
- Create context persistence across sessions
- Add session history search and retrieval

**Implementation:**
```go
// internal/memory/session_memory.go
package memory

type SessionMemory struct {
    SessionID   string
    Context     map[string]interface{}
    History     []Interaction
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

func LoadPreviousSession(sessionID string) (*SessionMemory, error)
func SaveSessionContext(session *SessionMemory) error
func SearchHistory(query string) ([]Interaction, error)
```

### 3. Limited Proactive Suggestions
**Current State:** Responds to directives rather than proactively suggesting improvements.

**Proposed Solution:**
- Implement code analysis module
- Add pattern detection for common issues
- Create suggestion engine

**Implementation:**
```go
// internal/suggestions/analyzer.go
package suggestions

type Suggestion struct {
    Type        string // "improvement", "security", "performance"
    Severity    string
    Description string
    Location    string
    AutoFix     bool
}

func AnalyzeCodebase(path string) ([]Suggestion, error)
func GenerateProactiveSuggestions(context map[string]interface{}) []Suggestion
```

### 4. Sequential Dependency Handling
**Current State:** Must wait for results before proceeding with dependent operations.

**Proposed Solution:**
- Implement asynchronous task execution
- Create dependency graph resolution
- Add parallel execution where possible

**Implementation:**
```go
// internal/execution/async_executor.go
package execution

type AsyncTask struct {
    ID           string
    Fn           func() (interface{}, error)
    Dependencies []string
    Result       chan interface{}
    Error        chan error
}

func ExecuteWithDependencies(tasks []AsyncTask) map[string]interface{}
```

### 5. Context Window Management
**Current State:** Token budget limits mean very long sessions could hit limits.

**Proposed Solution:**
- Implement context summarization
- Add smart context pruning
- Create context prioritization

**Implementation:**
```go
// internal/context/manager.go
package context

type ContextManager struct {
    MaxTokens     int
    CurrentTokens int
    Priority      map[string]int
}

func (cm *ContextManager) SummarizeContext() string
func (cm *ContextManager) PruneContext(keepEssential bool) error
func (cm *ContextManager) PrioritizeContext(items []string) []string
```

### 6. Enhanced Git Integration
**Current State:** Basic git operations work but could be more sophisticated.

**Proposed Solution:**
- Add intelligent commit message generation
- Implement branch management strategies
- Create PR description auto-generation

**Implementation:**
```go
// internal/git/smart_commit.go
package git

type CommitAnalyzer struct {
    Changes []FileChange
}

func (ca *CommitAnalyzer) GenerateCommitMessage() string
func (ca *CommitAnalyzer) GeneratePRDescription() string
func (ca *CommitAnalyzer) SuggestBranchName() string
```

### 7. Proactive File Discovery
**Current State:** Initially struggled to find files in the repository.

**Proposed Solution:**
- Implement smart file indexing on startup
- Create file type detection and categorization
- Add intelligent search suggestions

**Implementation:**
```go
// internal/indexer/file_indexer.go
package indexer

type FileIndex struct {
    Files      map[string]FileInfo
    ByType     map[string][]string
    ByLanguage map[string][]string
}

func BuildIndex(rootPath string) (*FileIndex, error)
func (fi *FileIndex) Search(pattern string) []string
func (fi *FileIndex) SuggestRelevantFiles(context string) []string
```

### 8. Enhanced Approval System
**Current State:** Manual intervention required for destructive operations.

**Proposed Solution:**
- Implement risk assessment for operations
- Add configurable approval policies
- Create operation simulation/preview

**Implementation:**
```go
// internal/approval/risk_assessor.go
package approval

type RiskLevel int

const (
    RiskNone RiskLevel = iota
    RiskLow
    RiskMedium
    RiskHigh
    RiskCritical
)

type Operation struct {
    Type        string
    Target      string
    Risk        RiskLevel
    Reversible  bool
    Preview     string
}

func AssessRisk(op Operation) RiskLevel
func RequiresApproval(op Operation, policy Policy) bool
func GeneratePreview(op Operation) string
```

## Implementation Roadmap

### Phase 1: Core Planning (Week 1-2)
- [ ] Implement basic `/plan` command
- [ ] Create task decomposition logic
- [ ] Add dependency resolution

### Phase 2: Memory & Context (Week 3-4)
- [ ] Build session memory storage
- [ ] Implement context persistence
- [ ] Add history search

### Phase 3: Proactive Analysis (Week 5-6)
- [ ] Create code analyzer
- [ ] Implement suggestion engine
- [ ] Add pattern detection

### Phase 4: Async Execution (Week 7-8)
- [ ] Build async task executor
- [ ] Implement dependency graph
- [ ] Add parallel execution

### Phase 5: Advanced Features (Week 9-10)
- [ ] Smart git integration
- [ ] File indexing
- [ ] Enhanced approval system
- [ ] Context management

## Testing Strategy

Each improvement should include:
1. Unit tests for core functionality
2. Integration tests with existing systems
3. End-to-end tests for user workflows
4. Performance benchmarks

## Success Metrics

- Planning: Can decompose complex tasks into 5+ subtasks
- Memory: Can recall context from previous sessions
- Suggestions: Generates 3+ relevant suggestions per codebase scan
- Async: Reduces execution time by 30% for parallel tasks
- Context: Maintains performance with 90%+ token utilization
- Git: Generates accurate commit messages 80%+ of the time
- Discovery: Finds relevant files within 2 seconds
- Approval: Correctly assesses risk in 95%+ of operations

## Notes

This is a living document and should be updated as implementation progresses and new weaknesses are identified.
