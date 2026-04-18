package curtailment

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/state"
)

// recipeLog records recipe calls for test assertions.
type recipeLog struct {
	calls []string
}

func (r *recipeLog) RunRecipe(_ context.Context, name string, _ []config.ActionStep) error {
	r.calls = append(r.calls, name)
	return nil
}

func (r *recipeLog) has(name string) bool {
	for _, c := range r.calls {
		if c == name {
			return true
		}
	}
	return false
}

func (r *recipeLog) reset() { r.calls = nil }

// stubSched implements ScheduleChecker for tests.
type stubSched struct {
	active string
}

func (s *stubSched) ActiveSchedule() string { return s.active }

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

var testLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

// --- ShouldRestore truth table ---

func TestShouldRestore(t *testing.T) {
	tests := []struct {
		name           string
		grid           state.Status
		vacation       bool
		schedule       bool
		wantRestore    bool
	}{
		{"normal operation", state.StatusNormal, false, false, true},
		{"grid curtailed", state.StatusCurtailed, false, false, false},
		{"vacation active", state.StatusNormal, true, false, false},
		{"schedule active", state.StatusNormal, false, true, false},
		{"grid down + vacation", state.StatusCurtailed, true, false, false},
		{"grid down + schedule", state.StatusCurtailed, false, true, false},
		{"vacation + schedule", state.StatusNormal, true, true, false},
		{"all holds active", state.StatusCurtailed, true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldRestore(tt.grid, tt.vacation, tt.schedule)
			if got != tt.wantRestore {
				t.Errorf("ShouldRestore(%v, %v, %v) = %v, want %v",
					tt.grid, tt.vacation, tt.schedule, got, tt.wantRestore)
			}
		})
	}
}

// --- VacationMonitor scenario tests ---

func newVacationMonitor(store *state.Store, eng *recipeLog, sched *stubSched) *VacationMonitor {
	return &VacationMonitor{
		Store: store,
		Eng:   eng,
		Sched: sched,
		Cfg: config.VacationConfig{
			Curtail: []config.ActionStep{{Action: "test.curtail"}},
			Restore: []config.ActionStep{{Action: "test.restore"}},
		},
		Log: testLog,
	}
}

func TestVacation_StartCurtailsWaterHeater(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	vm := newVacationMonitor(store, eng, &stubSched{})
	vm.Init()

	// Simulate ecobee reporting vacation active
	store.Set("ecobee.vacation_active", "true")
	result := vm.HandleTransition(context.Background())

	if result != "curtailed" {
		t.Fatalf("expected 'curtailed', got %q", result)
	}
	if !eng.has("vacation-curtail") {
		t.Fatal("expected vacation-curtail recipe to run")
	}
}

func TestVacation_EndRestoresWaterHeater(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	vm := newVacationMonitor(store, eng, &stubSched{})

	// Start in vacation
	store.Set("ecobee.vacation_active", "true")
	vm.Init()

	// Vacation ends
	store.Set("ecobee.vacation_active", "false")
	result := vm.HandleTransition(context.Background())

	if result != "restored" {
		t.Fatalf("expected 'restored', got %q", result)
	}
	if !eng.has("vacation-restore") {
		t.Fatal("expected vacation-restore recipe to run")
	}
}

func TestVacation_NoTransition_NoAction(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	vm := newVacationMonitor(store, eng, &stubSched{})
	vm.Init()

	// No change in vacation state
	result := vm.HandleTransition(context.Background())

	if result != "" {
		t.Fatalf("expected no action, got %q", result)
	}
	if len(eng.calls) != 0 {
		t.Fatalf("expected no recipe calls, got %v", eng.calls)
	}
}

func TestVacation_EndDuringGridDown_SkipsRestore(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	vm := newVacationMonitor(store, eng, &stubSched{})

	// Start in vacation with grid down
	store.Set("ecobee.vacation_active", "true")
	store.SetStatus(state.StatusCurtailed)
	vm.Init()

	// Vacation ends, but grid is still down
	store.Set("ecobee.vacation_active", "false")
	result := vm.HandleTransition(context.Background())

	if result != "skipped" {
		t.Fatalf("expected 'skipped', got %q", result)
	}
	if eng.has("vacation-restore") {
		t.Fatal("vacation-restore should NOT run while grid is curtailed")
	}
}

func TestVacation_EndDuringPeakSchedule_SkipsRestore(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	sched := &stubSched{active: "peak"}
	vm := newVacationMonitor(store, eng, sched)

	// Start in vacation
	store.Set("ecobee.vacation_active", "true")
	vm.Init()

	// Vacation ends, but peak schedule is active
	store.Set("ecobee.vacation_active", "false")
	result := vm.HandleTransition(context.Background())

	if result != "skipped" {
		t.Fatalf("expected 'skipped', got %q", result)
	}
	if eng.has("vacation-restore") {
		t.Fatal("vacation-restore should NOT run while peak schedule is active")
	}
}

func TestVacation_EndDuringMidPeakSchedule_SkipsRestore(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	sched := &stubSched{active: "mid-peak"}
	vm := newVacationMonitor(store, eng, sched)

	store.Set("ecobee.vacation_active", "true")
	vm.Init()

	store.Set("ecobee.vacation_active", "false")
	result := vm.HandleTransition(context.Background())

	if result != "skipped" {
		t.Fatalf("expected 'skipped', got %q", result)
	}
}

func TestVacation_GridRestoreWhileOnVacation_Curtail(t *testing.T) {
	// This tests the pattern used in main.go's grid restore handler:
	// after grid restore runs, vacation curtailment is reapplied.
	store := newTestStore(t)
	eng := &recipeLog{}
	vm := newVacationMonitor(store, eng, &stubSched{})

	store.Set("ecobee.vacation_active", "true")
	vm.Init()

	// Simulate: grid was down, now restored. The grid restore recipe
	// already ran (turning water heater on). We need vacation curtail
	// to re-run to turn it back off.
	store.SetStatus(state.StatusNormal)

	// No vacation transition happened (still on vacation), so
	// HandleTransition should return "" — the reapply logic
	// lives in main.go's OnTransition handler, not here.
	result := vm.HandleTransition(context.Background())
	if result != "" {
		t.Fatalf("expected no transition (still on vacation), got %q", result)
	}
}

// --- Full lifecycle scenario ---

func TestScenario_FullLifecycle(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	sched := &stubSched{}
	vm := newVacationMonitor(store, eng, sched)
	vm.Init()
	ctx := context.Background()

	// 1. Vacation starts
	store.Set("ecobee.vacation_active", "true")
	if r := vm.HandleTransition(ctx); r != "curtailed" {
		t.Fatalf("step 1: expected 'curtailed', got %q", r)
	}

	// 2. Grid goes down (vacation still active) — no vacation transition
	eng.reset()
	store.SetStatus(state.StatusCurtailed)
	if r := vm.HandleTransition(ctx); r != "" {
		t.Fatalf("step 2: expected no transition, got %q", r)
	}

	// 3. Grid restores (vacation still active) — no vacation transition
	// (main.go reapply logic would handle this separately)
	eng.reset()
	store.SetStatus(state.StatusNormal)
	if r := vm.HandleTransition(ctx); r != "" {
		t.Fatalf("step 3: expected no transition, got %q", r)
	}

	// 4. Peak schedule starts (vacation still active) — no vacation transition
	eng.reset()
	sched.active = "peak"
	if r := vm.HandleTransition(ctx); r != "" {
		t.Fatalf("step 4: expected no transition, got %q", r)
	}

	// 5. Vacation ends while peak schedule is active — skip restore
	eng.reset()
	store.Set("ecobee.vacation_active", "false")
	if r := vm.HandleTransition(ctx); r != "skipped" {
		t.Fatalf("step 5: expected 'skipped', got %q", r)
	}
	if eng.has("vacation-restore") {
		t.Fatal("step 5: vacation-restore should not run while peak is active")
	}

	// 6. Peak schedule ends, vacation is off, grid is up — nothing for vacation monitor
	eng.reset()
	sched.active = ""
	if r := vm.HandleTransition(ctx); r != "" {
		t.Fatalf("step 6: expected no transition, got %q", r)
	}
}

func TestScenario_VacationEndAfterAllClear(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	sched := &stubSched{}
	vm := newVacationMonitor(store, eng, sched)
	ctx := context.Background()

	// Grid down + vacation + peak
	store.SetStatus(state.StatusCurtailed)
	store.Set("ecobee.vacation_active", "true")
	sched.active = "peak"
	vm.Init()

	// Grid restores
	store.SetStatus(state.StatusNormal)
	vm.HandleTransition(ctx) // no transition
	eng.reset()

	// Peak ends
	sched.active = ""
	vm.HandleTransition(ctx) // no transition
	eng.reset()

	// Vacation ends — now all clear, should restore
	store.Set("ecobee.vacation_active", "false")
	r := vm.HandleTransition(ctx)
	if r != "restored" {
		t.Fatalf("expected 'restored', got %q", r)
	}
	if !eng.has("vacation-restore") {
		t.Fatal("expected vacation-restore to run when all holds are cleared")
	}
}

func TestVacation_NilScheduleChecker(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	vm := newVacationMonitor(store, eng, nil)
	vm.Sched = nil // no scheduler configured

	store.Set("ecobee.vacation_active", "true")
	vm.Init()

	store.Set("ecobee.vacation_active", "false")
	result := vm.HandleTransition(context.Background())

	if result != "restored" {
		t.Fatalf("expected 'restored' with nil scheduler, got %q", result)
	}
}

// --- FlowOverride tests ---

func newTestFlowOverride(store *state.Store, eng *recipeLog) *FlowOverride {
	return &FlowOverride{
		Store:   store,
		Eng:     eng,
		Curtail: []config.ActionStep{{Action: "test.curtail", FlowOverride: true}},
		Restore: []config.ActionStep{{Action: "test.restore", FlowOverride: true}},
		Log:     testLog,
		Label:   "grid",
		StatusCheck: func() bool {
			return store.GetStatus() == state.StatusCurtailed
		},
	}
}

func TestFlowOverride_RestoresWhenFlowDetected(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	fo := newTestFlowOverride(store, eng)

	store.SetStatus(state.StatusCurtailed)
	store.Set("flow.flowing", "true")
	fo.Evaluate(context.Background())

	if !fo.Active {
		t.Fatal("flow override should be active")
	}
	if !eng.has("flow-override:grid") {
		t.Fatal("expected flow-override:grid recipe to run")
	}
}

func TestFlowOverride_RecurtailsWhenFlowStops(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	fo := newTestFlowOverride(store, eng)

	store.SetStatus(state.StatusCurtailed)
	store.Set("flow.flowing", "true")
	fo.Evaluate(context.Background())

	eng.reset()
	store.Set("flow.flowing", "false")
	fo.Evaluate(context.Background())

	if fo.Active {
		t.Fatal("flow override should be inactive")
	}
	if !eng.has("flow-recurtail:grid") {
		t.Fatal("expected flow-recurtail:grid recipe to run")
	}
}

func TestFlowOverride_InactiveWhenStatusCheckFails(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	fo := newTestFlowOverride(store, eng)

	store.SetStatus(state.StatusNormal) // status check returns false
	store.Set("flow.flowing", "true")
	fo.Evaluate(context.Background())

	if fo.Active {
		t.Fatal("flow override should NOT activate when status check fails")
	}
}

func TestFlowOverride_ClearsWhenStatusCheckFails(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	fo := newTestFlowOverride(store, eng)

	store.SetStatus(state.StatusCurtailed)
	store.Set("flow.flowing", "true")
	fo.Evaluate(context.Background())
	if !fo.Active {
		t.Fatal("should be active")
	}

	eng.reset()
	store.SetStatus(state.StatusNormal)
	fo.Evaluate(context.Background())

	if fo.Active {
		t.Fatal("flow override should clear when status check fails")
	}
}

func TestFlowOverride_NoSteps_Noop(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	fo := &FlowOverride{
		Store: store,
		Eng:   eng,
		Log:   testLog,
		Label: "empty",
	}

	store.Set("flow.flowing", "true")
	fo.Evaluate(context.Background())

	if fo.Active {
		t.Fatal("should not activate with no steps")
	}
	if len(eng.calls) != 0 {
		t.Fatal("should not run any recipes with no steps")
	}
}
