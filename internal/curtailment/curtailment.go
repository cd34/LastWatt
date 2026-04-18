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

// ShouldFlowOverride returns true if flow-based override is allowed.
// Flow override is permitted during grid or rate curtailment, but NOT
// during vacation (nobody is home).
func ShouldFlowOverride(vacationActive bool, flowing bool) bool {
	return flowing && !vacationActive
}

// FlowOverride tracks flow-based water heater override state for grid
// curtailment. When the grid is down and flow is detected, the water heater
// temporarily restores. When flow stops, it re-curtails. Disabled during
// vacation.
type FlowOverride struct {
	Store   *state.Store
	Eng     RecipeRunner
	Curtail []config.ActionStep // actions to turn water heater off
	Restore []config.ActionStep // actions to turn water heater on
	Log     *slog.Logger
	Active  bool
}

// Evaluate checks current flow state and toggles the override. Should be
// called periodically (e.g., every 30s from the daemon loop or scheduler).
func (f *FlowOverride) Evaluate(ctx context.Context) {
	// Only relevant when grid is curtailed
	if f.Store.GetStatus() != state.StatusCurtailed {
		if f.Active {
			f.Active = false
		}
		return
	}

	// No flow override during vacation
	if v, _ := f.Store.Get("ecobee.vacation_active"); v == "true" {
		return
	}

	flowing, _ := f.Store.Get("flow.flowing")

	if flowing == "true" && !f.Active {
		f.Active = true
		f.Log.Info("flow detected during grid outage — temporarily restoring water heater")
		if err := f.Eng.RunRecipe(ctx, "grid-flow-override", f.Restore); err != nil {
			f.Log.Error("grid flow override restore failed", "error", err)
		}
	} else if flowing != "true" && f.Active {
		f.Active = false
		f.Log.Info("flow stopped during grid outage — re-curtailing water heater")
		if err := f.Eng.RunRecipe(ctx, "grid-flow-recurtail", f.Curtail); err != nil {
			f.Log.Error("grid flow re-curtail failed", "error", err)
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
