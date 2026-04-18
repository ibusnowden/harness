# API Client Architecture Review

**Date**: Current  
**Task**: #1 - Review current API client architecture and identify issues  
**Files Analyzed**: 
- `internal/api/client.go` (340 lines)
- `internal/api/openai.go` (368 lines)
- `internal/api/provider.go` (216 lines)
- `internal/api/types.go` (150 lines)
- `internal/api/provider_test.go` (378 lines)

## Current Architecture Overview

The API client consists of 5 Go files with ~1,452 total lines:

### File Responsibilities

1. **client.go**: Anthropic-specific client with SSE streaming
2. **openai.go**: OpenAI-compatible client (OpenAI, OpenRouter, xAI)
3. **provider.go**: Provider detection and client factory
4. **types.go**: Shared request/response types
5. **provider_test.go**: Integration tests

## Identified Issues

### 1. **Tight Coupling of Streaming Logic** (High Priority)
**Location**: `client.go` (lines 60-340), `openai.go` (lines 170-350)

**Problem**:
- SSE parsing is embedded directly in client implementations
- ~180 lines of streaming logic in `client.go`
- ~180 lines of OpenAI stream parsing in `openai.go`
- No code reuse between providers
- Difficult to test streaming logic independently

**Impact**:
- Code duplication
- Hard to add new streaming providers
- Testing requires full HTTP mocking
- Difficult to debug streaming issues

**Evidence**:
```go
// client.go has parseSSE function (50+ lines)
func parseSSE(body io.Reader, emit func(StreamEvent)) (MessageResponse, error)

// openai.go has parseOpenAIStream function (50+ lines)
func parseOpenAIStream(body io.Reader, emit func(StreamEvent)) (MessageResponse, error)
```

---

### 2. **Mixed Responsibilities in provider.go** (High Priority)
**Location**: `provider.go` (entire file)

**Problem**:
- Provider detection logic mixed with client creation
- Environment variable reading scattered throughout
- Provider routing rules embedded in multiple functions
- Configuration logic intertwined with business logic

**Impact**:
- Hard to understand provider selection rules
- Difficult to test provider routing independently
- Configuration changes require touching multiple areas
- No clear separation of concerns

**Evidence**:
```go
func providerKindForModel(model string, cfg ProviderConfig) ProviderKind {
    // Contains complex logic mixing model name parsing,
    // environment checks, and configuration preferences
}
```

---

### 3. **Inconsistent Error Handling** (Medium Priority)
**Location**: Throughout all client files

**Problem**:
- Different error message formats across providers
- Some errors include helpful context (OpenRouter 404), others don't
- No error types for programmatic handling
- Error wrapping inconsistent

**Impact**:
- Harder to handle errors at call sites
- Poor user experience with cryptic errors
- Difficult to add error recovery logic
- Testing error scenarios requires string matching

**Evidence**:
```go
// openai.go - Special case for OpenRouter 404
if response.StatusCode == 404 && c.kind == ProviderOpenRouter {
    return MessageResponse{}, fmt.Errorf(
        "model not found on OpenRouter: %s\n\nCheck available models...",
        msg,
    )
}

// client.go - Generic error
return MessageResponse{}, fmt.Errorf("anthropic request failed: %s: %s", ...)
```

---

### 4. **HTTP Client Management** (Medium Priority)
**Location**: `client.go` (lines 48-60), `provider.go` (lines 220-230)

**Problem**:
- `newHTTPClient` function creates clients with global state
- Transport override uses mutex-protected global variable
- Each client creates its own HTTP client
- No connection pooling or reuse strategy
- Testing requires global state modification

**Impact**:
- Potential race conditions in tests
- Resource inefficiency (multiple HTTP clients)
- Difficult to configure timeouts per-provider
- Global state makes concurrent testing harder

**Evidence**:
```go
var (
    transportOverrideMu sync.Mutex
    transportOverride   http.RoundTripper  // Global mutable state
)
```

---

### 5. **Duplication in Type Conversion** (Low Priority)
**Location**: `openai.go` (lines 70-165)

**Problem**:
- `toOpenAIRequest` and `convertOpenAIMessages` have overlapping logic
- JSON handling utilities scattered throughout
- No clear abstraction for format conversion

**Impact**:
- Code duplication
- Harder to maintain consistency
- Adding new providers requires reimplementing conversions

**Evidence**:
```go
// Multiple JSON helper functions doing similar things
func rawJSONOrEmptyObject(raw json.RawMessage) any
func compactRawJSON(raw json.RawMessage) string
func compactJSONString(value string) string
```

---

### 6. **Provider Detection Complexity** (Medium Priority)
**Location**: `provider.go` (lines 170-210)

**Problem**:
- `providerKindForModel` has complex nested logic
- Model name patterns hardcoded in multiple places
- Rules difficult to understand and maintain
- No documentation of routing logic

**Impact**:
- Hard to add new provider routing rules
- Easy to introduce bugs when changing logic
- Difficult for users to understand which provider will be used
- Testing requires comprehensive model name coverage

**Evidence**:
```go
// Complex nested logic
switch {
case shouldUseOpenRouter(normalized, cfg):
    return ProviderOpenRouter
case strings.Contains(normalized, "grok"):
    return ProviderXAI
case strings.HasPrefix(normalized, "gpt"), ...:
    return ProviderOpenAI
default:
    // More complex logic with anthropicAvailable check
}
```

---

### 7. **Testing Gaps** (Low Priority)
**Location**: `provider_test.go`

**Problem**:
- Tests focus on happy path integration scenarios
- No unit tests for individual components
- Streaming logic not tested in isolation
- Error handling paths not thoroughly tested
- No tests for edge cases (malformed SSE, partial data)

**Impact**:
- Lower confidence in edge case handling
- Difficult to refactor without integration test failures
- Slow test execution (requires full HTTP mocking)

---

## Architectural Smells

### 1. **God Object Pattern**
- `Client` and `OpenAICompatClient` do too much
- Mixing HTTP, streaming, parsing, and event emission

### 2. **Feature Envy**
- Provider detection logic "envies" configuration data
- Should be in a dedicated module

### 3. **Shotgun Surgery**
- Adding a new provider requires changes in multiple files
- No clear extension points

### 4. **Primitive Obsession**
- Heavy use of strings for provider kinds
- No rich error types
- Configuration passed as primitives

---

## Metrics

| Metric | Value | Assessment |
|--------|-------|------------|
| Lines per file | 150-378 | Too high for maintainability |
| Cyclomatic complexity (providerKindForModel) | ~8 | Moderate complexity |
| Code duplication | ~180 lines | High (streaming parsers) |
| Test coverage | ~70% (estimated) | Good but missing unit tests |
| Coupling | High | Modules tightly coupled |
| Cohesion | Low | Mixed responsibilities |

---

## Refactoring Priorities

### Must Fix (High Priority)
1. Extract streaming logic into separate module
2. Refactor provider detection into dedicated module
3. Improve error handling with custom types

### Should Fix (Medium Priority)
4. Refactor HTTP client management
5. Simplify provider routing logic
6. Add comprehensive error handling

### Nice to Have (Low Priority)
7. Extract type conversion utilities
8. Add unit tests for individual components
9. Document provider routing rules

---

## Proposed Module Structure

```
internal/api/
├── client.go              # Main client implementations (simplified)
├── types.go               # Shared types
├── streaming/             # NEW: Streaming logic
│   ├── sse.go            # SSE parser
│   ├── openai.go         # OpenAI stream parser
│   ├── assembler.go      # Response assembly
│   └── events.go         # Event emission
├── provider/              # NEW: Provider logic
│   ├── detector.go       # Model-to-provider routing
│   ├── factory.go        # Client creation
│   └── config.go         # Configuration handling
├── errors/                # NEW: Error types
│   └── errors.go         # Custom error types
└── http/                  # NEW: HTTP utilities
    └── client.go         # HTTP client management
```

---

## Recommendations

1. **Start with streaming extraction** - Highest code duplication
2. **Then provider refactoring** - Cleanest separation of concerns
3. **Add error types incrementally** - Can be done alongside other refactoring
4. **Improve HTTP client last** - Lowest immediate impact

---

## Success Criteria

After refactoring, the codebase should:
- ✅ Have streaming logic in a single, testable module
- ✅ Have clear provider detection rules
- ✅ Support adding new providers with minimal changes
- ✅ Have comprehensive unit tests for each module
- ✅ Have custom error types for better error handling
- ✅ Reduce file sizes to <200 lines each
- ✅ Achieve >85% test coverage
- ✅ Eliminate code duplication

---

## Next Steps

1. Review this analysis with team
2. Get approval for refactoring approach
3. Begin Task #2: Extract streaming logic
4. Proceed through remaining tasks in dependency order
