package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mcd/lastwatt/internal/actions"
	"github.com/mcd/lastwatt/internal/config"
)

// Engine executes recipe steps by looking up actions from the registry.
type Engine struct {
	store actions.StateStore
	log   *slog.Logger
}

func New(store actions.StateStore, log *slog.Logger) *Engine {
	return &Engine{store: store, log: log}
}

// RunRecipe executes a list of action steps in order.
// It stops on the first error and returns it.
func (e *Engine) RunRecipe(ctx context.Context, name string, steps []config.ActionStep) error {
	e.log.Info("running recipe", "recipe", name, "steps", len(steps))

	for i, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}

		a, err := actions.Get(step.Action)
		if err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}

		e.log.Info("executing action", "step", i+1, "action", step.Action, "params", step.Params)

		if err := a.Execute(ctx, step.Params, e.store); err != nil {
			e.log.Error("action failed", "step", i+1, "action", step.Action, "error", err)
			return fmt.Errorf("step %d (%s): %w", i+1, step.Action, err)
		}

		e.log.Info("action completed", "step", i+1, "action", step.Action)
	}

	e.log.Info("recipe completed", "recipe", name)
	return nil
}

// ValidateRecipe checks that all actions in a recipe are registered and their params are valid.
func (e *Engine) ValidateRecipe(name string, steps []config.ActionStep) error {
	for i, step := range steps {
		a, err := actions.Get(step.Action)
		if err != nil {
			return fmt.Errorf("recipe %q step %d: %w", name, i+1, err)
		}
		if err := a.Validate(step.Params); err != nil {
			return fmt.Errorf("recipe %q step %d (%s): %w", name, i+1, step.Action, err)
		}
	}
	return nil
}
