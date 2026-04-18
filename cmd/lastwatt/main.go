package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mcd/lastwatt/internal/actions"
	"github.com/mcd/lastwatt/internal/actions/ecobee"
	_ "github.com/mcd/lastwatt/internal/actions/gpio"
	_ "github.com/mcd/lastwatt/internal/actions/shelly"
	"github.com/mcd/lastwatt/internal/actions/tempest"
	"github.com/mcd/lastwatt/internal/config"
	"github.com/mcd/lastwatt/internal/curtailment"
	"github.com/mcd/lastwatt/internal/engine"
	"github.com/mcd/lastwatt/internal/forecast"
	"github.com/mcd/lastwatt/internal/monitor"
	"github.com/mcd/lastwatt/internal/scheduler"
	"github.com/mcd/lastwatt/internal/state"
)

var (
	cfgFile  string
	logLevel string
)

func main() {
	root := &cobra.Command{
		Use:   "lastwatt",
		Short: "Grid curtailment daemon for Raspberry Pi",
	}

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "config.yaml", "config file path")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")

	root.AddCommand(daemonCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(runCmd())
	root.AddCommand(actionCmd())
	root.AddCommand(ecobeeAuthCmd())
	root.AddCommand(forecastCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newLogger() *slog.Logger {
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func loadAll() (*config.Config, *state.Store, *engine.Engine, error) {
	log := newLogger()
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := state.New(cfg.StateFile)
	if err != nil {
		return nil, nil, nil, err
	}
	eng := engine.New(store, log)
	return cfg, store, eng, nil
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the curtailment monitor daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger()
			cfg, store, eng, err := loadAll()
			if err != nil {
				return err
			}

			// Validate recipes at startup
			if err := eng.ValidateRecipe("curtail", cfg.Grid.Curtail); err != nil {
				return fmt.Errorf("invalid curtail recipe: %w", err)
			}
			if err := eng.ValidateRecipe("restore", cfg.Grid.Restore); err != nil {
				return fmt.Errorf("invalid restore recipe: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// Reload state from disk on SIGUSR1 (sent by ecobee-auth after updating credentials)
			reloadCh := make(chan os.Signal, 1)
			signal.Notify(reloadCh, syscall.SIGUSR1)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-reloadCh:
						log.Info("received SIGUSR1, reloading state from disk")
						if err := store.Reload(); err != nil {
							log.Error("state reload failed", "error", err)
						} else {
							log.Info("state reloaded successfully")
						}
					}
				}
			}()

			// Start Tempest weather listener in background
			tl := tempest.GetListener(log)
			go func() {
				if err := tl.Run(ctx); err != nil {
					log.Error("tempest listener error", "error", err)
				}
			}()

			// Start NWS forecast provider in background
			if cfg.Location.Lat != 0 && cfg.Location.Lon != 0 {
				interval := cfg.Location.ForecastInterval
				if interval == 0 {
					interval = 30 * time.Minute
				}
				fp := forecast.NewProvider(cfg.Location.Lat, cfg.Location.Lon, log)
				go func() {
					if err := fp.Run(ctx, interval); err != nil {
						log.Error("forecast provider error", "error", err)
					}
				}()
			}

			// Start Ecobee keepalive to prevent OAuth session from going stale
			go ecobee.StartKeepAlive(ctx, 10*time.Minute, store, log)

			// Start schedule engine (includes any rate-based schedules)
			var sched *scheduler.Scheduler
			if len(cfg.Schedules) > 0 {
				sched = scheduler.New(cfg.Schedules, eng, store, log)
				sched.SetLocation(cfg.RatesLocation())
				if cfg.Rates.FlowOverride {
					sched.SetFlowOverride(true)
				}
				go sched.Run(ctx)
			}

			// Start grid flow override monitor if configured
			if cfg.Grid.FlowOverride {
				gridFlow := &curtailment.FlowOverride{
					Store:   store,
					Eng:     eng,
					Curtail: cfg.Grid.Curtail,
					Restore: cfg.Grid.Restore,
					Log:     log,
				}
				go func() {
					ticker := time.NewTicker(30 * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							gridFlow.Evaluate(ctx)
						}
					}
				}()
			}

			// Start vacation monitor if configured
			var vacMon *curtailment.VacationMonitor
			if len(cfg.Vacation.Curtail) > 0 || len(cfg.Vacation.Restore) > 0 {
				if err := eng.ValidateRecipe("vacation-curtail", cfg.Vacation.Curtail); err != nil {
					return fmt.Errorf("invalid vacation curtail recipe: %w", err)
				}
				if err := eng.ValidateRecipe("vacation-restore", cfg.Vacation.Restore); err != nil {
					return fmt.Errorf("invalid vacation restore recipe: %w", err)
				}
				vacInterval := cfg.Vacation.PollInterval
				if vacInterval == 0 {
					vacInterval = 10 * time.Minute
				}
				vacMon = &curtailment.VacationMonitor{
					Store: store,
					Eng:   eng,
					Sched: sched,
					Cfg:   cfg.Vacation,
					Log:   log,
				}
				vacMon.Init()
				go runVacationMonitor(ctx, vacInterval, vacMon, store, log)
			}

			mon := monitor.New(monitor.Config{
				Host:             cfg.Monitor.Host,
				Interval:         cfg.Monitor.Interval,
				FailThreshold:    cfg.Monitor.FailThreshold,
				RecoverThreshold: cfg.Monitor.RecoverThreshold,
				Log:              log,
				OnPing: func(ok bool) {
					if ok {
						store.SetLastPing(time.Now())
					}
				},
				OnTransition: func(from, to monitor.State) {
					switch to {
					case monitor.StateDown:
						log.Warn("grid power lost — running curtail recipe")
						if err := store.SetStatus(state.StatusCurtailed); err != nil {
							log.Error("failed to save state", "error", err)
						}
						go func() {
							if err := eng.RunRecipe(ctx, "curtail", cfg.Grid.Curtail); err != nil {
								log.Error("curtail recipe failed", "error", err)
							}
						}()
					case monitor.StateUp:
						log.Info("grid power restored — running restore recipe")
						if err := store.SetStatus(state.StatusNormal); err != nil {
							log.Error("failed to save state", "error", err)
						}
						go func() {
							if err := eng.RunRecipe(ctx, "restore", cfg.Grid.Restore); err != nil {
								log.Error("restore recipe failed", "error", err)
							}
							// If a schedule is active, reapply its actions
							// (e.g., keep water heater off during peak hours)
							if sched != nil {
								sched.ReapplyActive(ctx)
							}
							// If vacation mode is active, reapply vacation curtailment
							// (e.g., keep water heater off while away)
							if vacMon != nil {
								if v, _ := store.Get("ecobee.vacation_active"); v == "true" {
									log.Info("reapplying vacation curtailment after grid restore")
									if err := eng.RunRecipe(ctx, "vacation-curtail", cfg.Vacation.Curtail); err != nil {
										log.Error("vacation curtail reapply failed", "error", err)
									}
								}
							}
						}()
					}
				},
			})

			err = mon.Run(ctx)
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return err
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current curtailment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			store, err := state.New(cfg.StateFile)
			if err != nil {
				return err
			}
			fmt.Printf("Status:    %s\n", store.GetStatus())
			fmt.Printf("Since:     %s\n", store.Since().Format("2006-01-02 15:04:05"))
			fmt.Printf("Last ping: %s\n", store.LastPing().Format("2006-01-02 15:04:05"))
			return nil
		},
	}
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [curtail|restore]",
		Short: "Manually run a recipe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, eng, err := loadAll()
			if err != nil {
				return err
			}

			switch args[0] {
			case "curtail":
				return eng.RunRecipe(cmd.Context(), "curtail", cfg.Grid.Curtail)
			case "restore":
				return eng.RunRecipe(cmd.Context(), "restore", cfg.Grid.Restore)
			default:
				return fmt.Errorf("unknown recipe: %s (use 'curtail' or 'restore')", args[0])
			}
		},
	}
}

func actionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "action <name> [--param key=value ...]",
		Short: "Run a single action directly",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, _, err := loadAll()
			if err != nil {
				return err
			}

			a, err := actions.Get(args[0])
			if err != nil {
				return err
			}

			params := make(map[string]any)
			rawParams, _ := cmd.Flags().GetStringSlice("param")
			for _, p := range rawParams {
				parts := strings.SplitN(p, "=", 2)
				if len(parts) == 2 {
					params[parts[0]] = parts[1]
				}
			}

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			store, err := state.New(cfg.StateFile)
			if err != nil {
				return err
			}

			if err := a.Execute(cmd.Context(), params, store); err != nil {
				return err
			}
			return store.Flush()
		},
	}
	cmd.Flags().StringSliceP("param", "p", nil, "action parameters (key=value)")
	return cmd
}

func forecastCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forecast",
		Short: "Show current NWS hourly forecast",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := newLogger()
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			if cfg.Location.Lat == 0 || cfg.Location.Lon == 0 {
				return fmt.Errorf("location.lat and location.lon must be set in config")
			}

			fp := forecast.NewProvider(cfg.Location.Lat, cfg.Location.Lon, log)
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// Fetch once
			go fp.Run(ctx, 1*time.Hour)

			// Wait for data
			for {
				select {
				case <-ctx.Done():
					return fmt.Errorf("failed to fetch forecast")
				default:
					f := fp.Latest()
					if f == nil {
						time.Sleep(500 * time.Millisecond)
						continue
					}
					fmt.Printf("Forecast updated: %s\n", f.Updated.Format("2006-01-02 15:04"))
					fmt.Printf("Today's high (remaining): %d°F\n\n", f.TodayHigh())
					now := time.Now()
					count := 0
					for _, p := range f.Periods {
						if p.StartTime.Before(now.Add(-1 * time.Hour)) {
							continue
						}
						fmt.Printf("  %s  %3d°F  %2d%% precip  wind %d mph %s  %s\n",
							p.StartTime.Format("3PM"),
							p.TempF,
							p.PrecipPct,
							p.WindSpeedMPH,
							p.WindDir,
							p.Short,
						)
						count++
						if count >= 24 {
							break
						}
					}
					return nil
				}
			}
		},
	}
}

func ecobeeAuthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ecobee-auth",
		Short: "Authenticate with Ecobee (OAuth PIN flow)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Delegate to ecobee package's auth flow
			a, err := actions.Get("ecobee.auth")
			if err != nil {
				return fmt.Errorf("ecobee module not available: %w", err)
			}
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			store, err := state.New(cfg.StateFile)
			if err != nil {
				return err
			}
			if err := a.Execute(cmd.Context(), nil, store); err != nil {
				return err
			}
			if err := store.Flush(); err != nil {
				return err
			}
			// Signal the running daemon to reload state from disk
			signalDaemon(syscall.SIGUSR1)
			return nil
		},
	}
}

// runVacationMonitor periodically checks Ecobee vacation status and
// curtails/restores the water heater on transitions.
func runVacationMonitor(ctx context.Context, interval time.Duration, vacMon *curtailment.VacationMonitor, store *state.Store, log *slog.Logger) {
	log.Info("vacation monitor starting", "interval", interval)

	timer := time.NewTimer(interval) // first check after one interval (keepalive fires immediately)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("vacation monitor stopped")
			return
		case <-timer.C:
			// Read current thermostat state (sets ecobee.vacation_active in store)
			readMode, err := actions.Get("ecobee.read_mode")
			if err != nil {
				log.Error("vacation monitor: ecobee.read_mode not registered", "error", err)
				timer.Reset(interval)
				continue
			}
			if err := readMode.Execute(ctx, nil, store); err != nil {
				log.Warn("vacation monitor: failed to read thermostat", "error", err)
				timer.Reset(interval)
				continue
			}

			vacMon.HandleTransition(ctx)
			timer.Reset(interval)
		}
	}
}

// signalDaemon sends a signal to any running lastwatt daemon process.
func signalDaemon(sig syscall.Signal) {
	myPID := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(e.Name(), "%d", &pid); err != nil || pid == myPID {
			continue
		}
		cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			continue
		}
		// cmdline is null-separated; check if this is a lastwatt daemon process
		parts := strings.Split(string(cmdline), "\x00")
		if len(parts) >= 2 && strings.HasSuffix(parts[0], "lastwatt") && parts[1] == "daemon" {
			proc, err := os.FindProcess(pid)
			if err == nil {
				proc.Signal(sig)
				fmt.Printf("Signaled running daemon (PID %d) to reload state.\n", pid)
			}
			return
		}
	}
}
