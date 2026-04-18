package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ascaris/internal/api"
	"ascaris/internal/config"
	"ascaris/internal/subagents"
	"ascaris/internal/tasks"
	"ascaris/internal/tools"
)

type alwaysApprovePrompter struct{}

func (alwaysApprovePrompter) Approve(string, string) (bool, error) {
	return true, nil
}

func TestRunPromptExecutesApprovedPlanInternally(t *testing.T) {
	callCount := 0
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		return sseResponse(planApprovalToolUseSSE()), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	summary, err := harness.RunPrompt(context.Background(), "implement the plan", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Prompter:       alwaysApprovePrompter{},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected only the initial orchestrator request, got %d", callCount)
	}
	if !strings.Contains(summary.Message, "no subagent assignments") {
		t.Fatalf("expected internal plan execution summary, got %q", summary.Message)
	}
}

func TestPlanApprovalSkipsOtherToolCallsInSameResponse(t *testing.T) {
	callCount := 0
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		return sseResponse(planApprovalWithWriteFileToolUseSSE()), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)

	harness, err := NewLiveHarness(root)
	if err != nil {
		t.Fatalf("new live harness: %v", err)
	}
	summary, err := harness.RunPrompt(context.Background(), "implement the plan", PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		Prompter:       alwaysApprovePrompter{},
	})
	if err != nil {
		t.Fatalf("run prompt: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected only one model request, got %d", callCount)
	}
	if _, err := os.Stat(filepath.Join(root, "should_not_exist.txt")); !os.IsNotExist(err) {
		t.Fatalf("write_file should not have executed; stat err=%v", err)
	}
	var skipped tools.LiveResult
	found := false
	for _, result := range summary.ToolResults {
		if result.Name == "write_file" {
			skipped = result
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a skipped write_file result, got %#v", summary.ToolResults)
	}
	if !skipped.IsError || !strings.Contains(skipped.Output, "request_plan_approval must be the only tool executed") {
		t.Fatalf("unexpected skipped tool result: %#v", skipped)
	}
}

func TestSubagentExecutionUsesAssignmentAllowlistInsteadOfOrchestratorTools(t *testing.T) {
	callCount := 0
	var firstRequest api.MessageRequest
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload api.MessageRequest
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		switch callCount {
		case 1:
			firstRequest = payload
			return sseResponse(toolUseSSEForRuntime("toolu_read_fixture", "read_file", `{"path":"fixture.txt"}`)), nil
		case 2:
			return sseResponse(finalTextSSEEscaped(`{"summary":"read complete","verification":"read_file succeeded","blockers":[],"inspected_files":["fixture.txt"]}`)), nil
		default:
			t.Fatalf("unexpected HTTP call %d", callCount)
			return nil, nil
		}
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("fixture body\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	liveRuntime, err := newLiveRuntime(root, config.Empty(), PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		MaxIterations:  4,
	}, "parent session secret")
	if err != nil {
		t.Fatalf("new live runtime: %v", err)
	}
	defer liveRuntime.close()
	liveRuntime.orchestratorTools = []string{"delegate_task", "subagent_get", "subagent_list", "request_plan_approval"}

	blocked := liveRuntime.ExecuteTool(context.Background(), tools.LiveCall{
		ID:    "tool_read_blocked",
		Name:  "read_file",
		Input: json.RawMessage(`{"path":"fixture.txt"}`),
	}, 1)
	if !blocked.IsError || !strings.Contains(blocked.Output, "allowed tool list") {
		t.Fatalf("expected orchestrator-mode read_file denial, got %#v", blocked)
	}

	registry := subagents.NewRegistry()
	assignment, err := registry.Create("worker-1", "explorer", "inspect fixture", "read only", []string{"read_file"}, []string{"Inspect fixture.txt"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := subagents.SaveRegistry(root, registry); err != nil {
		t.Fatalf("save registry: %v", err)
	}

	completed, err := liveRuntime.runSubagentAssignment(context.Background(), assignment)
	if err != nil {
		t.Fatalf("run subagent assignment: %v", err)
	}
	if completed.Status != subagents.StatusCompleted {
		t.Fatalf("expected completed assignment, got %#v", completed)
	}
	if !containsString(completed.InspectedFiles, "fixture.txt") {
		t.Fatalf("expected inspected file to be tracked, got %#v", completed.InspectedFiles)
	}
	if len(firstRequest.Messages) != 1 {
		t.Fatalf("expected assignment-only message history, got %#v", firstRequest.Messages)
	}
	userPrompt := firstRequest.Messages[0].Content[0].Text
	if strings.Contains(userPrompt, "parent session secret") {
		t.Fatalf("subagent request leaked parent prompt: %q", userPrompt)
	}
	toolNames := map[string]bool{}
	for _, definition := range firstRequest.Tools {
		toolNames[definition.Name] = true
	}
	if !toolNames["read_file"] {
		t.Fatalf("expected subagent tool definitions to include read_file, got %v", toolNames)
	}
	for _, forbidden := range []string{"delegate_task", "subagent_get", "request_plan_approval"} {
		if toolNames[forbidden] {
			t.Fatalf("subagent should not inherit orchestrator tool %q; got %v", forbidden, toolNames)
		}
	}
}

func TestDelegateTaskRunsSubagentSynchronouslyWithLiveRunner(t *testing.T) {
	callCount := 0
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		callCount++
		switch callCount {
		case 1:
			return sseResponse(toolUseSSEForRuntime("toolu_read_fixture", "read_file", `{"path":"fixture.txt"}`)), nil
		case 2:
			return sseResponse(finalTextSSEEscaped(`{"summary":"read complete","verification":"read_file succeeded","blockers":[],"inspected_files":["fixture.txt"]}`)), nil
		default:
			t.Fatalf("unexpected HTTP call %d", callCount)
			return nil, nil
		}
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("fixture body\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	liveRuntime, err := newLiveRuntime(root, config.Empty(), PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
		MaxIterations:  4,
	}, "delegate a task")
	if err != nil {
		t.Fatalf("new live runtime: %v", err)
	}
	defer liveRuntime.close()

	input, _ := json.Marshal(map[string]any{
		"role":                "explorer",
		"prompt":              "inspect fixture",
		"context":             "read fixture.txt",
		"allowed_tools":       []string{"read_file"},
		"acceptance_criteria": []string{"Inspect fixture.txt"},
	})
	result := tools.ExecuteLive(tools.LiveContext{
		Root:           root,
		Context:        context.Background(),
		PermissionMode: tools.PermissionWorkspaceWrite,
		DelegateTask:   liveRuntime.runSubagentAssignment,
	}, tools.LiveCall{
		ID:    "delegate_1",
		Name:  "delegate_task",
		Input: input,
	})
	if result.IsError {
		t.Fatalf("delegate_task failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, `"status":"completed"`) || !strings.Contains(result.Output, `"result_summary":"read complete"`) {
		t.Fatalf("unexpected delegate_task output: %s", result.Output)
	}
	assignments, err := subagentAssignments(root)
	if err != nil {
		t.Fatalf("load assignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].Status != subagents.StatusCompleted {
		t.Fatalf("unexpected assignments: %#v", assignments)
	}
}

func TestExecuteApprovedPlanReopensFailedTaskForRetry(t *testing.T) {
	restoreTransport := api.SetTransportForTesting(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return sseResponse(finalTextSSEEscaped(`{"summary":"broken","verification":"missing blockers"}`)), nil
	}))
	defer restoreTransport()

	root := t.TempDir()
	configHome := filepath.Join(t.TempDir(), ".ascaris")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_BASE_URL", "https://mock.anthropic.local")
	t.Setenv("ASCARIS_CONFIG_HOME", configHome)
	if err := tasks.Replace(root, []tasks.Task{
		{
			ID:                 1,
			Title:              "Inspect fixture",
			Goal:               "Read fixture.txt",
			AcceptanceCriteria: []string{"Inspect fixture.txt"},
			AllowedTools:       []string{"read_file"},
			Status:             tasks.StatusOpen,
		},
		{
			ID:                 2,
			Title:              "Validate result",
			Goal:               "Summarize verification",
			AcceptanceCriteria: []string{"Validation is summarized"},
			AllowedTools:       []string{"read_file"},
			Status:             tasks.StatusOpen,
			BlockedBy:          []int{1},
		},
	}); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	liveRuntime, err := newLiveRuntime(root, config.Empty(), PromptOptions{
		Model:          "sonnet",
		PermissionMode: tools.PermissionWorkspaceWrite,
	}, "approved plan")
	if err != nil {
		t.Fatalf("new live runtime: %v", err)
	}
	defer liveRuntime.close()

	summary, err := liveRuntime.executeApprovedPlan(context.Background())
	if err == nil || !strings.Contains(err.Error(), "task #1 failed") {
		t.Fatalf("expected plan execution failure, got summary=%#v err=%v", summary, err)
	}

	taskList, loadErr := tasks.Load(root)
	if loadErr != nil {
		t.Fatalf("load tasks: %v", loadErr)
	}
	if len(taskList.Tasks) != 2 {
		t.Fatalf("unexpected task list: %#v", taskList.Tasks)
	}
	if taskList.Tasks[0].Status != tasks.StatusOpen {
		t.Fatalf("expected failed task to reopen for retry, got %#v", taskList.Tasks[0])
	}
	if !strings.Contains(taskList.Tasks[0].Notes, "task #1 failed") {
		t.Fatalf("expected failure note on reopened task, got %#v", taskList.Tasks[0])
	}
	if taskList.Tasks[1].Status != tasks.StatusOpen {
		t.Fatalf("expected blocked downstream task to remain open, got %#v", taskList.Tasks[1])
	}
	if !strings.Contains(summary.Message, "Plan execution stopped early") {
		t.Fatalf("expected failure summary, got %q", summary.Message)
	}
}

func planApprovalToolUseSSE() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_plan","name":"request_plan_approval","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"summary\":\"ready to begin\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func planApprovalWithWriteFileToolUseSSE() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_plan","name":"request_plan_approval","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"summary\":\"ready to begin\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_write","name":"write_file","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"should_not_exist.txt\",\"content\":\"nope\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func toolUseSSEForRuntime(id, name, input string) string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + mustJSONString(input) + `}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":4}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func finalTextSSEEscaped(text string) string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_final","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + mustJSONString(text) + `}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":11,"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
}

func mustJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
