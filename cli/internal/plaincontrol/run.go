package plaincontrol

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

const (
	defaultPollInterval      = 200 * time.Millisecond
	defaultHeartbeatInterval = 15 * time.Second
	failureRecapLimit        = 10
	maxFailureReasonLength   = 120
)

type Source interface {
	GetRunSnapshot(ctx context.Context, runID string, opts runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error)
}

type Config struct {
	RunID             string
	RunDir            string
	Source            Source
	Out               io.Writer
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	Now               func() time.Time
}

type Outcome struct {
	FinalRun store.Run
	Aborted  bool
}

type failureRecap struct {
	Ordinal int
	CaseID  string
}

type reporter struct {
	cfg Config

	lastRun          store.Run
	haveRun          bool
	startPrinted     bool
	lastCounts       domain.RunCounts
	haveCounts       bool
	lastRunState     domain.RunState
	lastProgressTime time.Time
	seenFailures     map[string]struct{}
	failures         []failureRecap
}

func Run(ctx context.Context, cfg Config) (Outcome, error) {
	if strings.TrimSpace(cfg.RunID) == "" {
		return Outcome{}, fmt.Errorf("run id is required")
	}
	if cfg.Source == nil {
		return Outcome{}, fmt.Errorf("plain-control source is required")
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &reporter{
		cfg:          cfg,
		seenFailures: map[string]struct{}{},
	}
	return r.run(ctx)
}

func (r *reporter) run(ctx context.Context) (Outcome, error) {
	for {
		snapshot, err := r.cfg.Source.GetRunSnapshot(ctx, r.cfg.RunID, runnerapi.SnapshotOptions{
			IncludeInstanceResults: true,
		})
		if err != nil {
			if !r.haveRun {
				return Outcome{}, fmt.Errorf("load run snapshot: %w", err)
			}
		} else {
			r.lastRun = snapshot.Run
			r.haveRun = true

			r.printStart(snapshot)
			r.printFailureLines(snapshot)
			if snapshot.Run.State.IsTerminal() {
				r.printFinalSummary(snapshot)
				return Outcome{FinalRun: snapshot.Run}, nil
			}
			r.printProgress(snapshot)
		}

		select {
		case <-ctx.Done():
			return Outcome{FinalRun: r.lastRun, Aborted: true}, nil
		case <-time.After(r.cfg.PollInterval):
		}
	}
}

func (r *reporter) printStart(snapshot runnerapi.RunSnapshot) {
	if r.startPrinted {
		return
	}
	fmt.Fprintf(r.cfg.Out, "[run] started run_id=%s total=%d run_dir=%s\n", snapshot.Run.RunID, len(snapshot.Instances), r.cfg.RunDir)
	r.startPrinted = true
}

func (r *reporter) printProgress(snapshot runnerapi.RunSnapshot) {
	now := r.cfg.Now().UTC()
	counts := snapshot.Run.Counts
	if !r.haveCounts || counts != r.lastCounts || snapshot.Run.State != r.lastRunState {
		r.emitProgressLine(snapshot.Run, now)
		r.lastCounts = counts
		r.lastRunState = snapshot.Run.State
		r.haveCounts = true
		r.lastProgressTime = now
		return
	}
	if r.lastProgressTime.IsZero() || now.Sub(r.lastProgressTime) >= r.cfg.HeartbeatInterval {
		r.emitProgressLine(snapshot.Run, now)
		r.lastProgressTime = now
	}
}

func (r *reporter) emitProgressLine(run store.Run, now time.Time) {
	counts := run.Counts
	total := counts.Pending + counts.Running + counts.Succeeded + counts.TestFailed + counts.InfraFailed + counts.Canceled
	completed := counts.Succeeded + counts.TestFailed + counts.InfraFailed + counts.Canceled
	fmt.Fprintf(
		r.cfg.Out,
		"[%s] progress %d/%d | running %d | pending %d | pass %d | test_fail %d | infra_fail %d\n",
		formatElapsed(run, now),
		completed,
		total,
		counts.Running,
		counts.Pending,
		counts.Succeeded,
		counts.TestFailed,
		counts.InfraFailed,
	)
}

func (r *reporter) printFailureLines(snapshot runnerapi.RunSnapshot) {
	for _, item := range snapshot.Instances {
		if !item.Instance.State.IsFailure() {
			continue
		}
		if _, exists := r.seenFailures[item.Instance.InstanceID]; exists {
			continue
		}
		r.seenFailures[item.Instance.InstanceID] = struct{}{}
		r.failures = append(r.failures, failureRecap{
			Ordinal: item.Instance.Ordinal,
			CaseID:  caseIDOrFallback(item.Instance.Case.CaseID),
		})
		fmt.Fprintf(
			r.cfg.Out,
			"[%s] fail #%03d %s %s\n",
			formatElapsed(snapshot.Run, r.cfg.Now().UTC()),
			item.Instance.Ordinal,
			caseIDOrFallback(item.Instance.Case.CaseID),
			describeFailure(item),
		)
	}
}

func (r *reporter) printFinalSummary(snapshot runnerapi.RunSnapshot) {
	run := snapshot.Run
	counts := run.Counts
	total := counts.Pending + counts.Running + counts.Succeeded + counts.TestFailed + counts.InfraFailed + counts.Canceled
	fmt.Fprintf(r.cfg.Out, "[%s] finished state=%s elapsed=%s\n", formatElapsed(run, runEnd(run, r.cfg.Now().UTC())), run.State, formatElapsed(run, runEnd(run, r.cfg.Now().UTC())))
	fmt.Fprintf(
		r.cfg.Out,
		"[%s] summary total=%d pass=%d test_fail=%d infra_fail=%d canceled=%d\n",
		formatElapsed(run, runEnd(run, r.cfg.Now().UTC())),
		total,
		counts.Succeeded,
		counts.TestFailed,
		counts.InfraFailed,
		counts.Canceled,
	)
	if len(r.failures) == 0 {
		return
	}
	sort.Slice(r.failures, func(i, j int) bool {
		if r.failures[i].Ordinal == r.failures[j].Ordinal {
			return r.failures[i].CaseID < r.failures[j].CaseID
		}
		return r.failures[i].Ordinal < r.failures[j].Ordinal
	})
	parts := make([]string, 0, minInt(len(r.failures), failureRecapLimit))
	for i, item := range r.failures {
		if i >= failureRecapLimit {
			break
		}
		parts = append(parts, fmt.Sprintf("#%03d %s", item.Ordinal, item.CaseID))
	}
	extra := len(r.failures) - len(parts)
	if extra > 0 {
		parts = append(parts, fmt.Sprintf("(+%d more)", extra))
	}
	fmt.Fprintf(r.cfg.Out, "[%s] failures %s\n", formatElapsed(run, runEnd(run, r.cfg.Now().UTC())), strings.Join(parts, ", "))
}

func describeFailure(item runnerapi.InstanceSnapshot) string {
	parts := []string{fmt.Sprintf("type=%s", item.Instance.State)}
	if item.Result == nil {
		return strings.Join(parts, " ")
	}
	if item.Result.TestExitCode != nil {
		parts = append(parts, fmt.Sprintf("test_exit=%d", *item.Result.TestExitCode))
	}
	if code := strings.TrimSpace(item.Result.ErrorCode); code != "" {
		parts = append(parts, fmt.Sprintf("code=%s", code))
	}
	if msg := strings.TrimSpace(item.Result.ErrorMessage); msg != "" {
		parts = append(parts, fmt.Sprintf("reason=%q", truncate(msg, maxFailureReasonLength)))
	}
	return strings.Join(parts, " ")
}

func caseIDOrFallback(caseID string) string {
	trimmed := strings.TrimSpace(caseID)
	if trimmed == "" {
		return "(no-case-id)"
	}
	return trimmed
}

func runEnd(run store.Run, now time.Time) time.Time {
	if run.EndedAt != nil && !run.EndedAt.IsZero() {
		return run.EndedAt.UTC()
	}
	return now
}

func formatElapsed(run store.Run, now time.Time) string {
	start := run.CreatedAt
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		start = *run.StartedAt
	}
	if start.IsZero() {
		return "0s"
	}
	end := runEnd(run, now)
	if end.Before(start) {
		return "0s"
	}
	return formatDuration(end.Sub(start))
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	if d < time.Second {
		return "0s"
	}
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	parts := make([]string, 0, 3)
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, "")
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
