package policy

import "time"

type GreenLevel uint8

const StaleBranchThreshold = time.Hour

type ConditionKind string

const (
	ConditionAnd            ConditionKind = "and"
	ConditionOr             ConditionKind = "or"
	ConditionGreenAt        ConditionKind = "green_at"
	ConditionStaleBranch    ConditionKind = "stale_branch"
	ConditionStartupBlocked ConditionKind = "startup_blocked"
	ConditionLaneCompleted  ConditionKind = "lane_completed"
	ConditionLaneReconciled ConditionKind = "lane_reconciled"
	ConditionReviewPassed   ConditionKind = "review_passed"
	ConditionScopedDiff     ConditionKind = "scoped_diff"
	ConditionTimedOut       ConditionKind = "timed_out"
)

type ActionKind string

const (
	ActionMergeToDev     ActionKind = "merge_to_dev"
	ActionMergeForward   ActionKind = "merge_forward"
	ActionRecoverOnce    ActionKind = "recover_once"
	ActionEscalate       ActionKind = "escalate"
	ActionCloseoutLane   ActionKind = "closeout_lane"
	ActionCleanupSession ActionKind = "cleanup_session"
	ActionReconcile      ActionKind = "reconcile"
	ActionNotify         ActionKind = "notify"
	ActionBlock          ActionKind = "block"
	ActionChain          ActionKind = "chain"
)

type ReconcileReason string

const (
	ReconcileAlreadyMerged ReconcileReason = "already_merged"
	ReconcileSuperseded    ReconcileReason = "superseded"
	ReconcileEmptyDiff     ReconcileReason = "empty_diff"
	ReconcileManualClose   ReconcileReason = "manual_close"
)

type LaneBlocker string

const (
	LaneBlockerNone     LaneBlocker = "none"
	LaneBlockerStartup  LaneBlocker = "startup"
	LaneBlockerExternal LaneBlocker = "external"
)

type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

type DiffScope string

const (
	DiffScopeFull   DiffScope = "full"
	DiffScopeScoped DiffScope = "scoped"
)

type Rule struct {
	Name      string    `json:"name"`
	Condition Condition `json:"condition"`
	Action    Action    `json:"action"`
	Priority  uint32    `json:"priority"`
}

type Condition struct {
	Kind       ConditionKind `json:"kind"`
	Conditions []Condition   `json:"conditions,omitempty"`
	Level      GreenLevel    `json:"level,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
}

type Action struct {
	Kind            ActionKind      `json:"kind"`
	Reason          string          `json:"reason,omitempty"`
	Channel         string          `json:"channel,omitempty"`
	ReconcileReason ReconcileReason `json:"reconcile_reason,omitempty"`
	Actions         []Action        `json:"actions,omitempty"`
}

type LaneContext struct {
	LaneID          string        `json:"lane_id"`
	GreenLevel      GreenLevel    `json:"green_level"`
	BranchFreshness time.Duration `json:"branch_freshness"`
	Blocker         LaneBlocker   `json:"blocker"`
	ReviewStatus    ReviewStatus  `json:"review_status"`
	DiffScope       DiffScope     `json:"diff_scope"`
	Completed       bool          `json:"completed"`
	Reconciled      bool          `json:"reconciled"`
}

type Engine struct {
	rules []Rule
}

func NewEngine(rules []Rule) Engine {
	sorted := append([]Rule(nil), rules...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Priority < sorted[i].Priority {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return Engine{rules: sorted}
}

func (e Engine) Rules() []Rule {
	return append([]Rule(nil), e.rules...)
}

func (e Engine) Evaluate(context LaneContext) []Action {
	actions := make([]Action, 0, len(e.rules))
	for _, rule := range e.rules {
		if rule.Condition.Matches(context) {
			rule.Action.flattenInto(&actions)
		}
	}
	return actions
}

func (c Condition) Matches(context LaneContext) bool {
	switch c.Kind {
	case ConditionAnd:
		for _, condition := range c.Conditions {
			if !condition.Matches(context) {
				return false
			}
		}
		return true
	case ConditionOr:
		for _, condition := range c.Conditions {
			if condition.Matches(context) {
				return true
			}
		}
		return false
	case ConditionGreenAt:
		return context.GreenLevel >= c.Level
	case ConditionStaleBranch:
		return context.BranchFreshness >= StaleBranchThreshold
	case ConditionStartupBlocked:
		return context.Blocker == LaneBlockerStartup
	case ConditionLaneCompleted:
		return context.Completed
	case ConditionLaneReconciled:
		return context.Reconciled
	case ConditionReviewPassed:
		return context.ReviewStatus == ReviewApproved
	case ConditionScopedDiff:
		return context.DiffScope == DiffScopeScoped
	case ConditionTimedOut:
		return context.BranchFreshness >= c.Duration
	default:
		return false
	}
}

func (a Action) flattenInto(actions *[]Action) {
	if a.Kind == ActionChain {
		for _, child := range a.Actions {
			child.flattenInto(actions)
		}
		return
	}
	*actions = append(*actions, a)
}

func And(conditions ...Condition) Condition {
	return Condition{Kind: ConditionAnd, Conditions: append([]Condition(nil), conditions...)}
}

func Or(conditions ...Condition) Condition {
	return Condition{Kind: ConditionOr, Conditions: append([]Condition(nil), conditions...)}
}

func GreenAt(level GreenLevel) Condition {
	return Condition{Kind: ConditionGreenAt, Level: level}
}

func StaleBranch() Condition {
	return Condition{Kind: ConditionStaleBranch}
}

func StartupBlocked() Condition {
	return Condition{Kind: ConditionStartupBlocked}
}

func LaneCompleted() Condition {
	return Condition{Kind: ConditionLaneCompleted}
}

func LaneReconciled() Condition {
	return Condition{Kind: ConditionLaneReconciled}
}

func ReviewPassed() Condition {
	return Condition{Kind: ConditionReviewPassed}
}

func ScopedDiff() Condition {
	return Condition{Kind: ConditionScopedDiff}
}

func TimedOut(duration time.Duration) Condition {
	return Condition{Kind: ConditionTimedOut, Duration: duration}
}

func MergeToDev() Action {
	return Action{Kind: ActionMergeToDev}
}

func MergeForward() Action {
	return Action{Kind: ActionMergeForward}
}

func RecoverOnce() Action {
	return Action{Kind: ActionRecoverOnce}
}

func Escalate(reason string) Action {
	return Action{Kind: ActionEscalate, Reason: reason}
}

func CloseoutLane() Action {
	return Action{Kind: ActionCloseoutLane}
}

func CleanupSession() Action {
	return Action{Kind: ActionCleanupSession}
}

func Reconcile(reason ReconcileReason) Action {
	return Action{Kind: ActionReconcile, ReconcileReason: reason}
}

func Notify(channel string) Action {
	return Action{Kind: ActionNotify, Channel: channel}
}

func Block(reason string) Action {
	return Action{Kind: ActionBlock, Reason: reason}
}

func Chain(actions ...Action) Action {
	return Action{Kind: ActionChain, Actions: append([]Action(nil), actions...)}
}
