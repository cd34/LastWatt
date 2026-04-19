package trigger

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/state"
)

// RecipeRunner executes a named recipe.
type RecipeRunner interface {
	RunRecipe(ctx context.Context, name string, steps []config.ActionStep) error
}

// StateProvider reads and writes state values.
type StateProvider interface {
	Get(key string) (string, bool)
	Set(key string, value string) error
	GetStatus() state.Status
}

// HoldChecker reports whether any system hold is active.
type HoldChecker interface {
	GridCurtailed() bool
	ScheduleActive() bool
	VacationActive() bool
}

type triggerState struct {
	cfg        config.TriggerConfig
	conditions []Condition
	active     bool
}

// Runner evaluates condition-based triggers and runs start/stop recipes
// on transitions. Follows the same pattern as the scheduler.
type Runner struct {
	triggers []triggerState
	eng      RecipeRunner
	store    StateProvider
	holds    HoldChecker
	log      *slog.Logger
}

// New creates a Runner and parses all trigger conditions upfront.
// Returns an error if any condition expression is malformed.
func New(cfgs []config.TriggerConfig, eng RecipeRunner, store StateProvider, holds HoldChecker, log *slog.Logger) (*Runner, error) {
	triggers := make([]triggerState, len(cfgs))
	for i, cfg := range cfgs {
		conds := make([]Condition, len(cfg.When))
		for j, expr := range cfg.When {
			c, err := ParseCondition(expr)
			if err != nil {
				return nil, fmt.Errorf("trigger %q condition %d: %w", cfg.Name, j+1, err)
			}
			conds[j] = c
		}
		triggers[i] = triggerState{cfg: cfg, conditions: conds}
	}
	return &Runner{
		triggers: triggers,
		eng:      eng,
		store:    store,
		holds:    holds,
		log:      log,
	}, nil
}

// Run evaluates triggers every 30 seconds. Blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	r.log.Info("trigger runner starting", "triggers", len(r.triggers))

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	r.Evaluate(ctx)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("trigger runner stopped")
			return
		case <-ticker.C:
			r.Evaluate(ctx)
		}
	}
}

// Evaluate checks all trigger conditions and fires start/stop on transitions.
func (r *Runner) Evaluate(ctx context.Context) {
	for i := range r.triggers {
		t := &r.triggers[i]
		met := EvaluateAll(t.conditions, r.store.Get)

		if met && !t.active {
			t.active = true
			r.log.Info("trigger fired", "trigger", t.cfg.Name)
			r.store.Set("trigger."+t.cfg.Name, "active")
			if err := r.eng.RunRecipe(ctx, "trigger:"+t.cfg.Name, t.cfg.Start); err != nil {
				r.log.Error("trigger start failed", "trigger", t.cfg.Name, "error", err)
			}
		} else if !met && t.active {
			t.active = false
			r.store.Set("trigger."+t.cfg.Name, "")

			if r.shouldSkipStop(t.cfg) {
				r.log.Info("trigger cleared but skipping stop — hold active",
					"trigger", t.cfg.Name)
				continue
			}

			r.log.Info("trigger cleared", "trigger", t.cfg.Name)
			if err := r.eng.RunRecipe(ctx, "trigger-stop:"+t.cfg.Name, t.cfg.Stop); err != nil {
				r.log.Error("trigger stop failed", "trigger", t.cfg.Name, "error", err)
			}
		}
	}
}

// ReapplyActive re-runs start recipes for any currently active triggers.
func (r *Runner) ReapplyActive(ctx context.Context) {
	for _, t := range r.triggers {
		if t.active {
			r.log.Info("reapplying trigger after grid restore", "trigger", t.cfg.Name)
			if err := r.eng.RunRecipe(ctx, "trigger:"+t.cfg.Name, t.cfg.Start); err != nil {
				r.log.Error("trigger reapply failed", "trigger", t.cfg.Name, "error", err)
			}
		}
	}
}

func (r *Runner) shouldSkipStop(cfg config.TriggerConfig) bool {
	if cfg.RespectHolds != nil && !*cfg.RespectHolds {
		return false
	}
	return r.holds.GridCurtailed() || r.holds.ScheduleActive() || r.holds.VacationActive()
}
