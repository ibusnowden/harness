package stalebranch

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type FreshnessKind string

const (
	FreshnessFresh    FreshnessKind = "fresh"
	FreshnessStale    FreshnessKind = "stale"
	FreshnessDiverged FreshnessKind = "diverged"
)

type BranchFreshness struct {
	Kind          FreshnessKind `json:"kind"`
	CommitsBehind int           `json:"commits_behind,omitempty"`
	Ahead         int           `json:"ahead,omitempty"`
	Behind        int           `json:"behind,omitempty"`
	MissingFixes  []string      `json:"missing_fixes,omitempty"`
}

type Policy string

const (
	PolicyAutoRebase       Policy = "auto_rebase"
	PolicyAutoMergeForward Policy = "auto_merge_forward"
	PolicyWarnOnly         Policy = "warn_only"
	PolicyBlock            Policy = "block"
)

type ActionKind string

const (
	ActionNoop         ActionKind = "noop"
	ActionWarn         ActionKind = "warn"
	ActionBlock        ActionKind = "block"
	ActionRebase       ActionKind = "rebase"
	ActionMergeForward ActionKind = "merge_forward"
)

type Action struct {
	Kind    ActionKind `json:"kind"`
	Message string     `json:"message,omitempty"`
}

func CheckFreshness(branch, mainRef, repoPath string) BranchFreshness {
	repoPath = filepath.Clean(repoPath)
	behind := revListCount(mainRef, branch, repoPath)
	ahead := revListCount(branch, mainRef, repoPath)
	if behind == 0 {
		return BranchFreshness{Kind: FreshnessFresh}
	}
	missingFixes := missingFixSubjects(mainRef, branch, repoPath)
	if ahead > 0 {
		return BranchFreshness{
			Kind:         FreshnessDiverged,
			Ahead:        ahead,
			Behind:       behind,
			MissingFixes: missingFixes,
		}
	}
	return BranchFreshness{
		Kind:          FreshnessStale,
		CommitsBehind: behind,
		MissingFixes:  missingFixes,
	}
}

func ApplyPolicy(freshness BranchFreshness, policy Policy) Action {
	switch freshness.Kind {
	case FreshnessFresh:
		return Action{Kind: ActionNoop}
	case FreshnessStale:
		switch policy {
		case PolicyWarnOnly:
			return Action{Kind: ActionWarn, Message: "Branch is " + strconv.Itoa(freshness.CommitsBehind) + " commit(s) behind main. Missing fixes: " + formatMissingFixes(freshness.MissingFixes)}
		case PolicyBlock:
			return Action{Kind: ActionBlock, Message: "Branch is " + strconv.Itoa(freshness.CommitsBehind) + " commit(s) behind main and must be updated before proceeding."}
		case PolicyAutoMergeForward:
			return Action{Kind: ActionMergeForward}
		default:
			return Action{Kind: ActionRebase}
		}
	case FreshnessDiverged:
		switch policy {
		case PolicyWarnOnly:
			return Action{Kind: ActionWarn, Message: "Branch has diverged: " + strconv.Itoa(freshness.Ahead) + " commit(s) ahead, " + strconv.Itoa(freshness.Behind) + " commit(s) behind main. Missing fixes: " + formatMissingFixes(freshness.MissingFixes)}
		case PolicyBlock:
			return Action{Kind: ActionBlock, Message: "Branch has diverged (" + strconv.Itoa(freshness.Ahead) + " ahead, " + strconv.Itoa(freshness.Behind) + " behind) and must be reconciled before proceeding. Missing fixes: " + formatMissingFixes(freshness.MissingFixes)}
		case PolicyAutoMergeForward:
			return Action{Kind: ActionMergeForward}
		default:
			return Action{Kind: ActionRebase}
		}
	default:
		return Action{Kind: ActionNoop}
	}
}

func formatMissingFixes(missingFixes []string) string {
	if len(missingFixes) == 0 {
		return "(none)"
	}
	return strings.Join(missingFixes, "; ")
}

func revListCount(a, b, repoPath string) int {
	command := exec.Command("git", "rev-list", "--count", b+".."+a)
	command.Dir = repoPath
	output, err := command.CombinedOutput()
	if err != nil {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0
	}
	return parsed
}

func missingFixSubjects(a, b, repoPath string) []string {
	command := exec.Command("git", "log", "--format=%s", b+".."+a)
	command.Dir = repoPath
	output, err := command.CombinedOutput()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
