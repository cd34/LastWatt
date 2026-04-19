package trigger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/state"
)

var testLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

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

type stubHolds struct {
	grid     bool
	schedule bool
	vacation bool
}

func (h *stubHolds) GridCurtailed() bool  { return h.grid }
func (h *stubHolds) ScheduleActive() bool { return h.schedule }
func (h *stubHolds) VacationActive() bool { return h.vacation }

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

func boolPtr(v bool) *bool { return &v }

func TestTrigger_EntersOnConditionMet(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{{
		Name:  "hot",
		When:  []string{"tempest.temp_f > 90"},
		Start: []config.ActionStep{{Action: "test.start"}},
		Stop:  []config.ActionStep{{Action: "test.stop"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())

	if !eng.has("trigger:hot") {
		t.Fatal("expected trigger:hot start recipe to run")
	}
	if v, _ := store.Get("trigger.hot"); v != "active" {
		t.Fatalf("expected trigger.hot = 'active', got %q", v)
	}
}

func TestTrigger_LeavesOnConditionCleared(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{{
		Name:  "hot",
		When:  []string{"tempest.temp_f > 90"},
		Start: []config.ActionStep{{Action: "test.start"}},
		Stop:  []config.ActionStep{{Action: "test.stop"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())

	eng.reset()
	store.Set("tempest.temp_f", "85")
	runner.Evaluate(context.Background())

	if !eng.has("trigger-stop:hot") {
		t.Fatal("expected trigger-stop:hot recipe to run")
	}
	if v, _ := store.Get("trigger.hot"); v != "" {
		t.Fatalf("expected trigger.hot = '', got %q", v)
	}
}

func TestTrigger_NoTransitionWhenStable(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{{
		Name:  "hot",
		When:  []string{"tempest.temp_f > 90"},
		Start: []config.ActionStep{{Action: "test.start"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())
	eng.reset()

	// Still 95 — no re-fire
	runner.Evaluate(context.Background())
	if len(eng.calls) != 0 {
		t.Fatalf("expected no calls on stable state, got %v", eng.calls)
	}
}

func TestTrigger_StopSkippedDuringGridCurtailment(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{grid: true}

	runner, err := New([]config.TriggerConfig{{
		Name:  "hot",
		When:  []string{"tempest.temp_f > 90"},
		Start: []config.ActionStep{{Action: "test.start"}},
		Stop:  []config.ActionStep{{Action: "test.stop"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())

	eng.reset()
	store.Set("tempest.temp_f", "85")
	runner.Evaluate(context.Background())

	if eng.has("trigger-stop:hot") {
		t.Fatal("stop should be skipped during grid curtailment")
	}
}

func TestTrigger_StopSkippedDuringVacation(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{vacation: true}

	runner, err := New([]config.TriggerConfig{{
		Name:  "hot",
		When:  []string{"tempest.temp_f > 90"},
		Start: []config.ActionStep{{Action: "test.start"}},
		Stop:  []config.ActionStep{{Action: "test.stop"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())

	eng.reset()
	store.Set("tempest.temp_f", "85")
	runner.Evaluate(context.Background())

	if eng.has("trigger-stop:hot") {
		t.Fatal("stop should be skipped during vacation")
	}
}

func TestTrigger_StopRunsWhenRespectHoldsFalse(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{grid: true, vacation: true, schedule: true}

	runner, err := New([]config.TriggerConfig{{
		Name:         "indicator",
		When:         []string{"tempest.temp_f > 90"},
		Start:        []config.ActionStep{{Action: "test.start"}},
		Stop:         []config.ActionStep{{Action: "test.stop"}},
		RespectHolds: boolPtr(false),
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())

	eng.reset()
	store.Set("tempest.temp_f", "85")
	runner.Evaluate(context.Background())

	if !eng.has("trigger-stop:indicator") {
		t.Fatal("stop should run when respect_holds is false")
	}
}

func TestTrigger_KeyMissing_DoesNotFire(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{{
		Name:  "missing",
		When:  []string{"nonexistent.key > 50"},
		Start: []config.ActionStep{{Action: "test.start"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	runner.Evaluate(context.Background())

	if len(eng.calls) != 0 {
		t.Fatal("should not fire when key is missing from store")
	}
}

func TestTrigger_MultipleConditions_AND(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{{
		Name: "heat_warning",
		When: []string{
			"tempest.temp_f > 90",
			"ecobee.saved_mode == heat",
		},
		Start: []config.ActionStep{{Action: "test.start"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	// Only one condition met
	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())
	if len(eng.calls) != 0 {
		t.Fatal("should not fire with only one condition met")
	}

	// Both conditions met
	store.Set("ecobee.saved_mode", "heat")
	runner.Evaluate(context.Background())
	if !eng.has("trigger:heat_warning") {
		t.Fatal("should fire when both conditions met")
	}
}

func TestTrigger_ReapplyActive(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{{
		Name:  "hot",
		When:  []string{"tempest.temp_f > 90"},
		Start: []config.ActionStep{{Action: "test.start"}},
	}}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("tempest.temp_f", "95")
	runner.Evaluate(context.Background())
	eng.reset()

	runner.ReapplyActive(context.Background())
	if !eng.has("trigger:hot") {
		t.Fatal("expected trigger to be reapplied")
	}
}

func TestTrigger_InvalidCondition(t *testing.T) {
	_, err := New([]config.TriggerConfig{{
		Name: "bad",
		When: []string{"no_operator_here"},
	}}, nil, nil, nil, testLog)
	if err == nil {
		t.Fatal("expected error for invalid condition")
	}
}

func TestTrigger_EvaluationOrder(t *testing.T) {
	store := newTestStore(t)
	eng := &recipeLog{}
	holds := &stubHolds{}

	runner, err := New([]config.TriggerConfig{
		{Name: "first", When: []string{"a == 1"}, Start: []config.ActionStep{{Action: "test.start"}}},
		{Name: "second", When: []string{"a == 1"}, Start: []config.ActionStep{{Action: "test.start"}}},
	}, eng, store, holds, testLog)
	if err != nil {
		t.Fatal(err)
	}

	store.Set("a", "1")
	runner.Evaluate(context.Background())

	if len(eng.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(eng.calls))
	}
	if eng.calls[0] != "trigger:first" || eng.calls[1] != "trigger:second" {
		t.Fatalf("expected [trigger:first, trigger:second], got %v", eng.calls)
	}
}
