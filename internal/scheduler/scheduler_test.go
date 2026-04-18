package scheduler

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mcd/lastwatt/internal/actions"
	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/engine"
	"github.com/mcd/lastwatt/internal/state"
)

var testLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

// recorderAction records calls for test assertions.
type recorderAction struct {
	name  string
	calls []map[string]any
}

func (a *recorderAction) Name() string                  { return a.name }
func (a *recorderAction) Validate(map[string]any) error { return nil }
func (a *recorderAction) Execute(_ context.Context, params map[string]any, _ actions.StateStore) error {
	a.calls = append(a.calls, params)
	return nil
}

func (a *recorderAction) callCount() int { return len(a.calls) }
func (a *recorderAction) reset()         { a.calls = nil }

func registerAction(name string, a *recorderAction) {
	actions.Register(a)
}

func unregisterAction(name string) {
	actions.Unregister(name)
}

func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := state.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testNow returns a stable reference time at noon to avoid midnight wraparound.
func testNow() time.Time {
	t := time.Now()
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, t.Location())
}

// scheduleAt creates a schedule active at the given time on the given weekday.
func scheduleAt(name string, ref time.Time) config.Schedule {
	day := ref.Weekday().String()[:3]
	start := ref.Add(-1 * time.Minute).Format("15:04")
	stop := ref.Add(1 * time.Hour).Format("15:04")
	return config.Schedule{
		Name:  name,
		Days:  []string{day},
		Start: start,
		Stop:  stop,
		Actions: []config.ActionStep{
			{Action: "test.curtail", Params: map[string]any{"label": name}},
		},
		Restore: []config.ActionStep{
			{Action: "test.restore", Params: map[string]any{"label": name}},
		},
	}
}

// scheduleExpiredAt creates a schedule that ended before ref.
func scheduleExpiredAt(name string, ref time.Time) config.Schedule {
	day := ref.Weekday().String()[:3]
	start := ref.Add(-2 * time.Hour).Format("15:04")
	stop := ref.Add(-1 * time.Minute).Format("15:04")
	return config.Schedule{
		Name:  name,
		Days:  []string{day},
		Start: start,
		Stop:  stop,
		Actions: []config.ActionStep{
			{Action: "test.curtail", Params: map[string]any{"label": name}},
		},
		Restore: []config.ActionStep{
			{Action: "test.restore", Params: map[string]any{"label": name}},
		},
	}
}

func newTestSched(t *testing.T, now time.Time, schedules []config.Schedule) (*Scheduler, *state.Store, *recorderAction, *recorderAction) {
	t.Helper()
	store := newTestStore(t)
	eng := engine.New(store, testLog)

	curtailAct := &recorderAction{name: "test.curtail"}
	restoreAct := &recorderAction{name: "test.restore"}
	registerAction("test.curtail", curtailAct)
	registerAction("test.restore", restoreAct)
	t.Cleanup(func() {
		unregisterAction("test.curtail")
		unregisterAction("test.restore")
	})

	s := New(schedules, eng, store, testLog)
	s.now = func() time.Time { return now }
	return s, store, curtailAct, restoreAct
}

func TestSchedule_EnterRunsActions(t *testing.T) {
	now := testNow()
	sched, store, _, _ := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})

	ctx := context.Background()
	sched.evaluate(ctx)

	if sched.ActiveSchedule() != "peak" {
		t.Fatalf("expected 'peak' active, got %q", sched.ActiveSchedule())
	}
	if v, _ := store.Get("schedule.active"); v != "peak" {
		t.Fatalf("expected store schedule.active='peak', got %q", v)
	}
}

func TestSchedule_LeaveRunsRestore(t *testing.T) {
	now := testNow()
	sched, _, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	ctx := context.Background()

	// Enter the schedule
	sched.evaluate(ctx)
	if sched.ActiveSchedule() != "peak" {
		t.Fatal("schedule should be active")
	}

	// Simulate schedule window ending
	sched.schedules = []config.Schedule{scheduleExpiredAt("peak", now)}
	restoreAct.reset()
	sched.evaluate(ctx)

	if sched.ActiveSchedule() != "" {
		t.Fatalf("expected no active schedule, got %q", sched.ActiveSchedule())
	}
	if restoreAct.callCount() == 0 {
		t.Fatal("expected restore action to run")
	}
}

func TestSchedule_LeaveDuringGridCurtailment_SkipsRestore(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleExpiredAt("peak", now)})

	sched.active["peak"] = true
	store.SetStatus(state.StatusCurtailed)

	sched.evaluate(context.Background())

	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run while grid is curtailed")
	}
}

func TestSchedule_LeaveDuringVacation_SkipsRestore(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleExpiredAt("peak", now)})

	sched.active["peak"] = true
	store.Set("ecobee.vacation_active", "true")

	sched.evaluate(context.Background())

	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run while vacation is active")
	}
}

func TestSchedule_OverlappingMidPeakAndPeak(t *testing.T) {
	now := testNow()
	midPeak := scheduleAt("mid-peak", now)
	peak := scheduleAt("peak", now)

	sched, store, curtailAct, restoreAct := newTestSched(t, now, []config.Schedule{midPeak, peak})
	ctx := context.Background()

	// Both enter
	sched.evaluate(ctx)
	if curtailAct.callCount() != 2 {
		t.Fatalf("expected 2 curtail calls (mid-peak + peak), got %d", curtailAct.callCount())
	}

	// Mid-peak ends, but peak is still active
	sched.schedules[0] = scheduleExpiredAt("mid-peak", now)
	restoreAct.reset()
	sched.evaluate(ctx)

	if sched.ActiveSchedule() != "peak" {
		t.Fatalf("expected 'peak' still active, got %q", sched.ActiveSchedule())
	}
	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run — peak schedule is still active")
	}
	if v, _ := store.Get("schedule.active"); v != "peak" {
		t.Fatalf("store should show 'peak' active, got %q", v)
	}

	// Peak ends — now restore should run
	sched.schedules[1] = scheduleExpiredAt("peak", now)
	restoreAct.reset()
	sched.evaluate(ctx)

	if sched.ActiveSchedule() != "" {
		t.Fatalf("expected no active schedule, got %q", sched.ActiveSchedule())
	}
	if restoreAct.callCount() == 0 {
		t.Fatal("restore should run when last schedule ends")
	}
	if v, _ := store.Get("schedule.active"); v != "" {
		t.Fatalf("store should be cleared, got %q", v)
	}
}

func TestSchedule_OverlappingEndDuringVacation(t *testing.T) {
	now := testNow()
	midPeak := scheduleAt("mid-peak", now)
	peak := scheduleAt("peak", now)

	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{midPeak, peak})
	ctx := context.Background()

	// Both active + vacation
	sched.evaluate(ctx)
	store.Set("ecobee.vacation_active", "true")

	// Mid-peak ends — peak still active, skip restore
	sched.schedules[0] = scheduleExpiredAt("mid-peak", now)
	restoreAct.reset()
	sched.evaluate(ctx)
	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run — peak is still active")
	}

	// Peak ends — vacation still active, skip restore
	sched.schedules[1] = scheduleExpiredAt("peak", now)
	restoreAct.reset()
	sched.evaluate(ctx)
	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run — vacation is active")
	}
}

func TestSchedule_ReapplyAfterGridRestore(t *testing.T) {
	now := testNow()
	sched, _, curtailAct, _ := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	ctx := context.Background()

	// Enter schedule
	sched.evaluate(ctx)
	if sched.ActiveSchedule() != "peak" {
		t.Fatal("schedule should be active")
	}

	// Grid goes down and comes back — ReapplyActive should re-run actions
	curtailAct.reset()
	sched.ReapplyActive(ctx)

	if curtailAct.callCount() == 0 {
		t.Fatal("expected schedule actions to be reapplied after grid restore")
	}
}

// --- Flow override tests ---

func TestSchedule_FlowOverride_RestoresDuringSchedule(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	sched.SetFlowOverride(true)
	ctx := context.Background()

	// Enter schedule
	sched.evaluate(ctx)
	if sched.ActiveSchedule() != "peak" {
		t.Fatal("schedule should be active")
	}

	// Flow detected — should restore water heater temporarily
	restoreAct.reset()
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx)

	if !sched.FlowOverrideActive() {
		t.Fatal("flow override should be active")
	}
	if restoreAct.callCount() == 0 {
		t.Fatal("expected restore to run when flow detected during schedule")
	}
}

func TestSchedule_FlowOverride_RecurtailsWhenFlowStops(t *testing.T) {
	now := testNow()
	sched, store, curtailAct, _ := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	sched.SetFlowOverride(true)
	ctx := context.Background()

	// Enter schedule, start flow
	sched.evaluate(ctx)
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx) // triggers flow override

	// Flow stops — should re-curtail
	curtailAct.reset()
	store.Set("flow.flowing", "false")
	sched.evaluate(ctx)

	if sched.FlowOverrideActive() {
		t.Fatal("flow override should be inactive after flow stops")
	}
	if curtailAct.callCount() == 0 {
		t.Fatal("expected curtail to re-run when flow stops")
	}
}

func TestSchedule_FlowOverride_BlockedDuringVacation(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	sched.SetFlowOverride(true)
	ctx := context.Background()

	// Enter schedule, vacation active
	sched.evaluate(ctx)
	store.Set("ecobee.vacation_active", "true")

	// Flow detected — should NOT override because vacation
	restoreAct.reset()
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx)

	if sched.FlowOverrideActive() {
		t.Fatal("flow override should NOT activate during vacation")
	}
	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run during vacation even with flow")
	}
}

func TestSchedule_FlowOverride_AllowedDuringVacation_WhenEnabled(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	sched.SetFlowOverride(true)
	sched.SetVacationFlowOverride(true)
	ctx := context.Background()

	sched.evaluate(ctx)
	store.Set("ecobee.vacation_active", "true")

	restoreAct.reset()
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx)

	if !sched.FlowOverrideActive() {
		t.Fatal("flow override should activate during vacation when enabled")
	}
	if restoreAct.callCount() == 0 {
		t.Fatal("expected restore to run")
	}
}

func TestSchedule_FlowOverride_DisabledByDefault(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	// flowOverride NOT set (default false)
	ctx := context.Background()

	sched.evaluate(ctx)
	store.Set("flow.flowing", "true")
	restoreAct.reset()
	sched.evaluate(ctx)

	if sched.FlowOverrideActive() {
		t.Fatal("flow override should not activate when disabled")
	}
	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run when flow override is disabled")
	}
}

func TestSchedule_FlowOverride_CleansUpWhenScheduleEnds(t *testing.T) {
	now := testNow()
	sched, store, _, _ := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	sched.SetFlowOverride(true)
	ctx := context.Background()

	// Enter, start flow override
	sched.evaluate(ctx)
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx)
	if !sched.FlowOverrideActive() {
		t.Fatal("flow override should be active")
	}

	// Schedule ends
	sched.schedules = []config.Schedule{scheduleExpiredAt("peak", now)}
	sched.evaluate(ctx)

	if sched.FlowOverrideActive() {
		t.Fatal("flow override should be cleared when schedule ends")
	}
}
