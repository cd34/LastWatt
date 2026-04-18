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
	begin := ref.Add(-1 * time.Minute).Format("15:04")
	end := ref.Add(1 * time.Hour).Format("15:04")
	return config.Schedule{
		Name:  name,
		Days:  []string{day},
		Begin: begin,
		End:   end,
		Start: []config.ActionStep{
			{Action: "test.curtail", Params: map[string]any{"label": name}},
		},
		Stop: []config.ActionStep{
			{Action: "test.restore", Params: map[string]any{"label": name}},
		},
	}
}

// scheduleAtWithFlow creates a schedule active at ref with flow_override on actions.
func scheduleAtWithFlow(name string, ref time.Time) config.Schedule {
	s := scheduleAt(name, ref)
	for i := range s.Start {
		s.Start[i].FlowOverride = true
	}
	for i := range s.Stop {
		s.Stop[i].FlowOverride = true
	}
	return s
}

// scheduleExpiredAt creates a schedule that ended before ref.
func scheduleExpiredAt(name string, ref time.Time) config.Schedule {
	day := ref.Weekday().String()[:3]
	begin := ref.Add(-2 * time.Hour).Format("15:04")
	end := ref.Add(-1 * time.Minute).Format("15:04")
	return config.Schedule{
		Name:  name,
		Days:  []string{day},
		Begin: begin,
		End:   end,
		Start: []config.ActionStep{
			{Action: "test.curtail", Params: map[string]any{"label": name}},
		},
		Stop: []config.ActionStep{
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

// --- Jitter tests ---

func TestSchedule_Jitter_OffsetsStartTime(t *testing.T) {
	now := testNow()
	// Create a schedule that starts exactly at now — without jitter it enters immediately
	day := now.Weekday().String()[:3]
	sched, store, curtailAct, _ := newTestSched(t, now, []config.Schedule{{
		Name:   "test",
		Days:   []string{day},
		Begin:  now.Format("15:04"),
		End:    now.Add(1 * time.Hour).Format("15:04"),
		Jitter: 15 * time.Minute,
		Start:  []config.ActionStep{{Action: "test.curtail"}},
		Stop:   []config.ActionStep{{Action: "test.restore"}},
	}})
	_ = store
	ctx := context.Background()

	// Force jitter to be computed
	sched.evaluate(ctx)

	// The jitter was computed — verify it's within range
	j := sched.jitter["test"]
	if j < -15*time.Minute || j > 15*time.Minute {
		t.Fatalf("jitter %v out of range [-15m, +15m]", j)
	}

	// If jitter is positive, the start shifts later so the schedule hasn't entered yet
	// If jitter is negative, start shifts earlier so it entered
	if j > 0 {
		if curtailAct.callCount() != 0 {
			t.Logf("jitter=%v (positive), schedule should not have entered yet", j)
			t.Fatal("curtail should not run with positive jitter")
		}
	}
	// We can't assert the negative case deterministically, just verify no crash
}

func TestSchedule_Jitter_RecomputesDaily(t *testing.T) {
	now := testNow()
	day := now.Weekday().String()[:3]
	sched, _, _, _ := newTestSched(t, now, []config.Schedule{{
		Name:   "test",
		Days:   []string{day},
		Begin:  now.Add(-30 * time.Minute).Format("15:04"),
		End:    now.Add(1 * time.Hour).Format("15:04"),
		Jitter: 5 * time.Minute,
		Start:  []config.ActionStep{{Action: "test.curtail"}},
		Stop:   []config.ActionStep{{Action: "test.restore"}},
	}})
	ctx := context.Background()

	sched.evaluate(ctx)
	firstJitter := sched.jitter["test"]
	firstDay := sched.jitterDay

	// Same day — jitter should not recompute
	sched.evaluate(ctx)
	if sched.jitter["test"] != firstJitter {
		t.Fatal("jitter should not change within the same day")
	}

	// Advance to next day
	tomorrow := now.Add(24 * time.Hour)
	sched.now = func() time.Time { return tomorrow }
	sched.evaluate(ctx)

	if sched.jitterDay == firstDay {
		t.Fatal("jitter day should have advanced")
	}
	// New jitter may or may not differ (random), but it was recomputed
}

// --- Flow override tests ---

func TestSchedule_FlowOverride_RestoresDuringSchedule(t *testing.T) {
	now := testNow()
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAtWithFlow("peak", now)})
	ctx := context.Background()

	sched.evaluate(ctx)
	if sched.ActiveSchedule() != "peak" {
		t.Fatal("schedule should be active")
	}

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
	sched, store, curtailAct, _ := newTestSched(t, now, []config.Schedule{scheduleAtWithFlow("peak", now)})
	ctx := context.Background()

	sched.evaluate(ctx)
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx)

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

func TestSchedule_FlowOverride_NotSetOnActions(t *testing.T) {
	now := testNow()
	// scheduleAt does NOT set flow_override on steps
	sched, store, _, restoreAct := newTestSched(t, now, []config.Schedule{scheduleAt("peak", now)})
	ctx := context.Background()

	sched.evaluate(ctx)
	store.Set("flow.flowing", "true")
	restoreAct.reset()
	sched.evaluate(ctx)

	if sched.FlowOverrideActive() {
		t.Fatal("flow override should not activate when no steps have flow_override")
	}
	if restoreAct.callCount() != 0 {
		t.Fatal("restore should NOT run when no steps have flow_override")
	}
}

func TestSchedule_FlowOverride_CleansUpWhenScheduleEnds(t *testing.T) {
	now := testNow()
	sched, store, _, _ := newTestSched(t, now, []config.Schedule{scheduleAtWithFlow("peak", now)})
	ctx := context.Background()

	sched.evaluate(ctx)
	store.Set("flow.flowing", "true")
	sched.evaluate(ctx)
	if !sched.FlowOverrideActive() {
		t.Fatal("flow override should be active")
	}

	sched.schedules = []config.Schedule{scheduleExpiredAt("peak", now)}
	sched.evaluate(ctx)

	if sched.FlowOverrideActive() {
		t.Fatal("flow override should be cleared when schedule ends")
	}
}
