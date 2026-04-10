package policy

import (
	"testing"
	"time"
)

func TestEngineEvaluatesPriorityOrderedRules(t *testing.T) {
	engine := NewEngine([]Rule{
		{
			Name:      "late-cleanup",
			Condition: And(),
			Action:    CleanupSession(),
			Priority:  30,
		},
		{
			Name:      "first-notify",
			Condition: And(),
			Action:    Notify("ops"),
			Priority:  10,
		},
		{
			Name:      "merge",
			Condition: And(),
			Action:    MergeToDev(),
			Priority:  20,
		},
	})
	actions := engine.Evaluate(LaneContext{})
	if len(actions) != 3 {
		t.Fatalf("expected three actions, got %d", len(actions))
	}
	if actions[0].Kind != ActionNotify || actions[1].Kind != ActionMergeToDev || actions[2].Kind != ActionCleanupSession {
		t.Fatalf("unexpected action ordering: %#v", actions)
	}
}

func TestStaleAndStartupConditionsMatch(t *testing.T) {
	context := LaneContext{
		LaneID:          "lane-7",
		GreenLevel:      3,
		BranchFreshness: 2 * time.Hour,
		Blocker:         LaneBlockerStartup,
		ReviewStatus:    ReviewApproved,
		DiffScope:       DiffScopeScoped,
	}
	if !StaleBranch().Matches(context) {
		t.Fatalf("expected stale branch condition to match")
	}
	if !StartupBlocked().Matches(context) {
		t.Fatalf("expected startup blocked condition to match")
	}
	if !And(GreenAt(2), ReviewPassed(), ScopedDiff()).Matches(context) {
		t.Fatalf("expected composed condition to match")
	}
}
