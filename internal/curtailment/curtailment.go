package curtailment

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/state"
)

// RecipeRunner executes a named recipe.
type RecipeRunner interface {
	RunRecipe(ctx context.Context, name string, steps []config.ActionStep) error
}

// ScheduleChecker reports whether any schedule is currently active.
type ScheduleChecker interface {
	ActiveSchedule() string
}

// ShouldRestore returns true only when no system needs the water heater off.
func ShouldRestore(gridStatus state.Status, vacationActive bool, scheduleActive bool) bool {
	return gridStatus != state.StatusCurtailed && !vacationActive && !scheduleActive
}

// FlowOverride tracks flow-based override state for actions marked with
// flow_override: true. When flow is detected during curtailment, those
// specific actions are temporarily restored. When flow stops, they
// re-curtail. Only operates while the given status check returns true.
type FlowOverride struct {
	Store      *state.Store
	Eng        RecipeRunner
	Curtail    []config.ActionStep // flow_override steps to re-curtail
	Restore    []config.ActionStep // flow_override steps to restore
	Log        *slog.Logger
	Label      string // e.g. "grid", "sched:peak" — for log/recipe names
	StatusCheck func() bool // returns true when this override should be evaluated
	Active     bool
}

// Evaluate checks current flow state and toggles the override.
func (f *FlowOverride) Evaluate(ctx context.Context) {
	if len(f.Curtail) == 0 && len(f.Restore) == 0 {
		return
	}

	if f.StatusCheck != nil && !f.StatusCheck() {
		if f.Active {
			f.Active = false
		}
		return
	}

	flowing, _ := f.Store.Get("flow.flowing")

	if flowing == "true" && !f.Active {
		f.Active = true
		f.Log.Info("flow detected — temporarily restoring flow_override actions", "source", f.Label)
		if err := f.Eng.RunRecipe(ctx, "flow-override:"+f.Label, f.Restore); err != nil {
			f.Log.Error("flow override restore failed", "source", f.Label, "error", err)
		}
	} else if flowing != "true" && f.Active {
		f.Active = false
		f.Log.Info("flow stopped — re-curtailing flow_override actions", "source", f.Label)
		if err := f.Eng.RunRecipe(ctx, "flow-recurtail:"+f.Label, f.Curtail); err != nil {
			f.Log.Error("flow re-curtail failed", "source", f.Label, "error", err)
		}
	}
}

// VacationMonitor checks for vacation mode transitions and curtails/restores
// the water heater accordingly.
type VacationMonitor struct {
	Store       *state.Store
	Eng         RecipeRunner
	Sched       ScheduleChecker
	Cfg         config.VacationConfig
	Log         *slog.Logger
	lastVacation string
}

// Init loads the initial vacation state from the store.
func (v *VacationMonitor) Init() {
	v.lastVacation, _ = v.Store.Get("ecobee.vacation_active")
}

// HandleTransition checks whether vacation state has changed and runs the
// appropriate curtail or restore recipe. Returns the actions taken.
func (v *VacationMonitor) HandleTransition(ctx context.Context) string {
	nowVacation, _ := v.Store.Get("ecobee.vacation_active")

	defer func() { v.lastVacation = nowVacation }()

	if nowVacation == "true" && v.lastVacation != "true" {
		v.Log.Info("vacation mode activated — running vacation curtail")
		if err := v.Eng.RunRecipe(ctx, "vacation-curtail", v.Cfg.Curtail); err != nil {
			v.Log.Error("vacation curtail failed", "error", err)
		}
		return "curtailed"
	}

	if nowVacation != "true" && v.lastVacation == "true" {
		schedActive := v.Sched != nil && v.Sched.ActiveSchedule() != ""
		if !ShouldRestore(v.Store.GetStatus(), false, schedActive) {
			reason := "grid curtailed"
			if v.Store.GetStatus() != state.StatusCurtailed {
				reason = fmt.Sprintf("schedule %q active", v.Sched.ActiveSchedule())
			}
			v.Log.Info("vacation mode ended but skipping restore", "reason", reason)
			return "skipped"
		}
		v.Log.Info("vacation mode ended — running vacation restore")
		if err := v.Eng.RunRecipe(ctx, "vacation-restore", v.Cfg.Restore); err != nil {
			v.Log.Error("vacation restore failed", "error", err)
		}
		return "restored"
	}

	return ""
}
