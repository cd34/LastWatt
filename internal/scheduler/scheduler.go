package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/engine"
	"github.com/mcd/lastwatt/internal/state"
)

var dayMap = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

// Scheduler evaluates time-based schedules and runs actions when entering
// or leaving schedule windows. It coordinates with grid curtailment state
// to avoid conflicting actions.
type Scheduler struct {
	schedules []config.Schedule
	eng       *engine.Engine
	store     *state.Store
	log       *slog.Logger
	active       map[string]bool
	now          func() time.Time // defaults to time.Now; override in tests
	loc          *time.Location   // timezone for schedule evaluation
	flowOverride         bool // allow flow to temporarily override rate curtailment
	flowActive           bool // tracks whether flow override is currently engaged
	vacationFlowOverride bool // if true, flow override works even during vacation
}

func New(schedules []config.Schedule, eng *engine.Engine, store *state.Store, log *slog.Logger) *Scheduler {
	return &Scheduler{
		schedules: schedules,
		eng:       eng,
		store:     store,
		log:       log,
		active:    make(map[string]bool),
		now:       time.Now,
	}
}

// SetLocation sets the timezone used for schedule time evaluation.
func (s *Scheduler) SetLocation(loc *time.Location) {
	s.loc = loc
}

// SetFlowOverride enables the flow-based water heater override during
// rate schedules. When flow is detected, the schedule restore recipe runs;
// when flow stops, the curtail recipe is reapplied.
func (s *Scheduler) SetFlowOverride(enabled bool) {
	s.flowOverride = enabled
}

// SetVacationFlowOverride controls whether flow override is allowed during
// vacation mode. Defaults to false.
func (s *Scheduler) SetVacationFlowOverride(enabled bool) {
	s.vacationFlowOverride = enabled
}

// ActiveSchedule returns the name of the currently active schedule, or "".
func (s *Scheduler) ActiveSchedule() string {
	for name, active := range s.active {
		if active {
			return name
		}
	}
	return ""
}

// ReapplyActive re-runs the actions for any currently active schedule.
// Called by the grid restore handler to reassert schedule state after
// grid power returns.
func (s *Scheduler) ReapplyActive(ctx context.Context) {
	for _, sched := range s.schedules {
		if s.active[sched.Name] {
			s.log.Info("reapplying schedule after grid restore", "schedule", sched.Name)
			if err := s.eng.RunRecipe(ctx, "sched:"+sched.Name, sched.Actions); err != nil {
				s.log.Error("schedule reapply failed", "schedule", sched.Name, "error", err)
			}
		}
	}
}

// Run evaluates schedules every 30 seconds. Blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.log.Info("scheduler starting", "schedules", len(s.schedules))

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Evaluate immediately
	s.evaluate(ctx)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.evaluate(ctx)
		}
	}
}

func (s *Scheduler) evaluate(ctx context.Context) {
	now := s.now()
	if s.loc != nil {
		now = now.In(s.loc)
	}

	for _, sched := range s.schedules {
		inWindow := s.inWindow(sched, now)
		wasActive := s.active[sched.Name]

		if inWindow && !wasActive {
			s.enter(ctx, sched)
		} else if !inWindow && wasActive {
			s.leave(ctx, sched)
		}
	}

	// Flow override: if a rate schedule is active and flow is detected,
	// temporarily restore the water heater. When flow stops, re-curtail.
	if s.flowOverride && s.ActiveSchedule() != "" {
		s.evaluateFlowOverride(ctx)
	} else if s.flowActive {
		// Schedule ended while flow override was engaged — clean up
		s.flowActive = false
	}
}

func (s *Scheduler) enter(ctx context.Context, sched config.Schedule) {
	s.log.Info("schedule entering window", "schedule", sched.Name)
	s.active[sched.Name] = true
	s.store.Set("schedule.active", sched.Name)

	if err := s.eng.RunRecipe(ctx, "sched:"+sched.Name, sched.Actions); err != nil {
		s.log.Error("schedule actions failed", "schedule", sched.Name, "error", err)
	}
}

func (s *Scheduler) leave(ctx context.Context, sched config.Schedule) {
	s.log.Info("schedule leaving window", "schedule", sched.Name)
	s.active[sched.Name] = false

	// Update store to reflect remaining active schedule (if any)
	if remaining := s.ActiveSchedule(); remaining != "" {
		s.store.Set("schedule.active", remaining)
		s.log.Info("skipping schedule restore — other schedule still active",
			"ended", sched.Name, "active", remaining)
		return
	}
	s.store.Set("schedule.active", "")

	// Don't restore if grid is curtailed — let grid restore handle it
	if s.store.GetStatus() == state.StatusCurtailed {
		s.log.Info("skipping schedule restore — grid is curtailed", "schedule", sched.Name)
		return
	}

	// Don't restore if vacation mode is active
	if v, _ := s.store.Get("ecobee.vacation_active"); v == "true" {
		s.log.Info("skipping schedule restore — vacation mode active", "schedule", sched.Name)
		return
	}

	if err := s.eng.RunRecipe(ctx, "sched-restore:"+sched.Name, sched.Restore); err != nil {
		s.log.Error("schedule restore failed", "schedule", sched.Name, "error", err)
	}
}

func (s *Scheduler) evaluateFlowOverride(ctx context.Context) {
	vacActive, _ := s.store.Get("ecobee.vacation_active")
	if vacActive == "true" && !s.vacationFlowOverride {
		return
	}

	flowing, _ := s.store.Get("flow.flowing")

	if flowing == "true" && !s.flowActive {
		// Flow just started — restore water heater temporarily
		s.flowActive = true
		s.log.Info("flow detected during rate schedule — temporarily restoring water heater")
		active := s.activeSchedule()
		if active != nil {
			if err := s.eng.RunRecipe(ctx, "flow-override:"+active.Name, active.Restore); err != nil {
				s.log.Error("flow override restore failed", "error", err)
			}
		}
	} else if flowing != "true" && s.flowActive {
		// Flow stopped — re-curtail
		s.flowActive = false
		s.log.Info("flow stopped — re-curtailing water heater")
		active := s.activeSchedule()
		if active != nil {
			if err := s.eng.RunRecipe(ctx, "flow-recurtail:"+active.Name, active.Actions); err != nil {
				s.log.Error("flow re-curtail failed", "error", err)
			}
		}
	}
}

// activeSchedule returns the config for the first currently active schedule.
func (s *Scheduler) activeSchedule() *config.Schedule {
	for i, sched := range s.schedules {
		if s.active[sched.Name] {
			return &s.schedules[i]
		}
	}
	return nil
}

// FlowOverrideActive reports whether the flow override is currently engaged.
func (s *Scheduler) FlowOverrideActive() bool {
	return s.flowActive
}

func (s *Scheduler) inWindow(sched config.Schedule, now time.Time) bool {
	if !s.matchesDay(sched.Days, now.Weekday()) {
		return false
	}

	start, err := parseTime(sched.Start, now)
	if err != nil {
		s.log.Error("bad schedule start time", "schedule", sched.Name, "error", err)
		return false
	}
	stop, err := parseTime(sched.Stop, now)
	if err != nil {
		s.log.Error("bad schedule stop time", "schedule", sched.Name, "error", err)
		return false
	}

	return !now.Before(start) && now.Before(stop)
}

func (s *Scheduler) matchesDay(days []string, weekday time.Weekday) bool {
	for _, d := range days {
		if mapped, ok := dayMap[strings.ToLower(d)]; ok && mapped == weekday {
			return true
		}
	}
	return false
}

// parseTime parses "HH:MM" into a time.Time on the same date as ref.
func parseTime(hhmm string, ref time.Time) (time.Time, error) {
	var h, m int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m); err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q: %w", hhmm, err)
	}
	return time.Date(ref.Year(), ref.Month(), ref.Day(), h, m, 0, 0, ref.Location()), nil
}
