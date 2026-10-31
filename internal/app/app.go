// Package app wires the CLI, sensor sampling and the panel together.
package app

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
	"github.com/spf13/pflag"

	"github.com/volcie/cooler-lcd-linux/internal/panel"
	"github.com/volcie/cooler-lcd-linux/internal/telemetry"
	"github.com/volcie/cooler-lcd-linux/internal/version"
)

// Options are the resolved command-line settings.
type Options struct {
	Device  string
	Refresh time.Duration
	Unit    TempUnit
	Mode    DisplayMode
	Cycle   time.Duration
	Target  ShowTarget
	Freq    FreqMode
	Verbose bool
	JSON    bool

	Paths telemetry.Paths
}

// New builds the root command.
var configFile string

func New() *cobra.Command {
	opts := Options{
		Unit:   Celsius,
		Mode:   ShowTemps,
		Cycle:  2 * time.Second,
		Target: ShowBoth,
		Freq:   FreqMax,
	}

	root := &cobra.Command{
		Use:   "cooler-lcd",
		Short: "Drive the LCD on a 5131:2007 CPU cooler",
		Long: `cooler-lcd drives the segmented display found on CPU coolers built around
the HID 5131:2007 controller (UPSIREN Walrus, Vevor/HT, Eagle Warrior and
rebrands).

The panel firmware draws its own digits; this streams sensor values to it every
200 ms. Run with no subcommand to stream until interrupted.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// The file supplies defaults; explicit flags win. Only flags the
			// user actually set are re-applied over the file's values.
			cfg, err := loadConfig(configFile)
			if err != nil {
				return fmt.Errorf("%w: %w", errConfig, err)
			}
			fromFlags := opts
			if err := opts.applyConfig(cfg); err != nil {
				return fmt.Errorf("%w: %w", errConfig, err)
			}
			cmd.Flags().Visit(func(f *pflag.Flag) {
				overlayFlag(&opts, fromFlags, f.Name)
			})
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStream(cmd, opts)
		},
	}

	// All flags are persistent so subcommands inherit them; anything else
	// makes "cooler-lcd once --fahrenheit" an unknown-flag error.
	pf := root.PersistentFlags()

	pf.StringVar(&configFile, "config", "", "config file (default /etc/cooler-lcd/config.toml)")

	// What the panel shows.
	pf.Var(&opts.Unit, "unit", "temperature scale: c or f")
	pf.Var(&opts.Mode, "show", "what to display: temp, usage or cycle")
	pf.DurationVar(&opts.Cycle, "cycle", 2*time.Second, "seconds per mode when --show=cycle")
	pf.Var(&opts.Target, "only", "which halves to populate: both, cpu or gpu")
	pf.Var(&opts.Freq, "freq", "CPU clock to report: max or avg")

	// Runtime.
	pf.StringVar(&opts.Device, "device", "", "hidraw node, e.g. /dev/hidraw2 (default: autodetect)")
	pf.DurationVar(&opts.Refresh, "refresh", 200*time.Millisecond, "frame interval")
	pf.BoolVar(&opts.Verbose, "verbose", false, "log every frame")
	pf.BoolVar(&opts.JSON, "log-json", false, "log as JSON")

	// Sensor overrides.
	pf.StringVar(&opts.Paths.CPUTemp, "cpu-temp", "", "sysfs temp*_input path for CPU temperature")
	pf.StringVar(&opts.Paths.GPU, "gpu-hwmon", "", "amdgpu hwmon directory")
	pf.StringVar(&opts.Paths.Fan, "fan-path", "", "sysfs fan*_input path for fan RPM")
	pf.StringVar(&opts.Paths.Pump, "pump-path", "", "sysfs fan*_input path for pump RPM")
	pf.StringVar(&opts.Paths.RAPL, "rapl", "", "sysfs energy_uj path for CPU power")

	root.AddCommand(
		newConfigCmd(&opts),
		newServiceCmd(),
		newInstallCmd(&opts),
		newPrintCmd(&opts),
		newOnceCmd(&opts),
		newListCmd(),
		newUninstallCmd(),
	)
	// Top-level shorthands so the common actions need no subcommand nesting.
	for _, action := range []string{"status", "start", "stop", "restart"} {
		action := action
		root.AddCommand(&cobra.Command{
			Use:   action,
			Short: shorthandHelp[action],
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if action == "status" {
					return serviceStatus(cmd)
				}
				return serviceAction(cmd, action)
			},
		})
	}
	return root
}

var shorthandHelp = map[string]string{
	"status":  "Show whether the service is running and what it reports",
	"start":   "Start the background service",
	"stop":    "Stop the background service",
	"restart": "Restart the background service (picks up config changes)",
}

// Execute runs the CLI, returning the process exit code.
func Execute() int {
	root := New()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if isPermanent(err) {
			return exitUsage
		}
		return exitError
	}
	return exitOK
}

// validate rejects settings the panel would misinterpret. Enum values are
// checked by their Set methods, so only ranges and paths are checked here.
func (o Options) validate() error {
	if o.Refresh <= 0 {
		return fmt.Errorf("--refresh must be positive")
	}
	if _, err := cycleSeconds(o.Cycle); err != nil {
		return fmt.Errorf("--cycle: %w", err)
	}
	// An explicitly configured sensor path that does not exist would otherwise
	// report 0 on the panel with no hint that anything is wrong.
	for _, p := range []struct{ name, path string }{
		{"--cpu-temp", o.Paths.CPUTemp},
		{"--fan-path", o.Paths.Fan},
		{"--pump-path", o.Paths.Pump},
		{"--rapl", o.Paths.RAPL},
	} {
		if p.path == "" {
			continue
		}
		if _, err := os.Stat(p.path); err != nil {
			return fmt.Errorf("%s: %s", p.name, err)
		}
	}
	if o.Paths.GPU != "" {
		if fi, err := os.Stat(o.Paths.GPU); err != nil {
			return fmt.Errorf("--gpu-hwmon: %w", err)
		} else if !fi.IsDir() {
			return fmt.Errorf("--gpu-hwmon: %s is not a directory", o.Paths.GPU)
		}
	}
	return nil
}

// display converts the friendly options into protocol values.
func (o Options) display() panel.Display {
	secs, _ := cycleSeconds(o.Cycle)
	return panel.Display{
		Mode:         o.Mode.wire(),
		CycleSeconds: secs,
		CPUGPU:       o.Target.wire(),
	}
}

// logger builds the structured logger once flags are parsed.
func (o Options) logger() *slog.Logger {
	level := slog.LevelInfo
	if o.Verbose {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}
	if o.JSON {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// readings samples every sensor once. CPU power and usage are deltas, so the
// caller primes them and waits before calling this.
func readings(s *telemetry.Sampler, o Options) panel.Readings {
	return panel.Readings{
		CPUTemp:    s.CPUTemp(),
		CPUUsage:   s.CPUUsage(),
		CPUPower:   s.CPUPower(),
		CPUFreqMHz: s.CPUFreqMHz(o.Freq == FreqAvg),
		CPUVolt:    s.CPUVolt(),
		GPUTemp:    s.GPUTemp(),
		GPUUsage:   s.GPUUsage(),
		GPUPower:   s.GPUPower(),
		GPUFreqMHz: s.GPUFreqMHz(),
		FanRPM:     s.FanRPM(),
		PumpRPM:    s.PumpRPM(),
		RAMUsage:   s.RAMPercent(),
		Fahrenheit: o.Unit == Fahrenheit,
		Now:        s.Now(),
	}
}

// Sender is the panel surface the streaming loop needs. Narrowing it to an
// interface keeps the loop testable without a real HID device.
type Sender interface {
	Send(payload []byte) error
	Path() string
	Close() error
}

// open connects to the panel. An explicit --device is honoured only if it is
// one of the nodes the controller actually exposes; anything else is refused
// rather than handed to hidapi, which would reinterpret it.
func open(o Options) (Sender, error) {
	if o.Device == "" {
		return panel.Open()
	}

	devices, err := panel.Enumerate()
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if d.Path == o.Device {
			return panel.OpenPath(d.Path)
		}
	}

	available := make([]string, 0, len(devices))
	for _, d := range devices {
		available = append(available, d.Path)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("no %04x:%04x device found; cannot use --device %s",
			panel.VendorID, panel.ProductID, o.Device)
	}
	return nil, fmt.Errorf("--device %s is not a cooler panel; available: %s",
		o.Device, strings.Join(available, ", "))
}

// runStream publishes frames until the context is cancelled, then blanks the
// panel the way the vendor app does on close.
func runStream(cmd *cobra.Command, o Options) error {
	if err := o.validate(); err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}
	log := o.logger()

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s := telemetry.New(o.Paths)

	dev, err := open(o)
	if err != nil {
		return err
	}
	defer dev.Close()

	// Prime the delta counters immediately before streaming so the very first
	// frame carries a real interval rather than the whole startup elapsed time.
	s.Prime()

	log.Info("panel connected", "node", dev.Path(), "refresh", o.Refresh.String(),
		"cpu_temp", s.Paths().CPUTemp, "gpu_hwmon", s.Paths().GPU, "fan", s.Paths().Fan)

	return stream(ctx, s, dev, o, log)
}

// stream publishes frames until ctx is cancelled, then blanks the panel the way
// the vendor app does on close.
func stream(ctx context.Context, s *telemetry.Sampler, dev Sender, o Options, log *slog.Logger) error {
	// Reused across frames: the send loop runs at 5 Hz for the life of the
	// service, so per-frame allocation is pure GC pressure.
	values := make([]byte, panel.ValueCount)
	frame := make([]byte, panel.ReportLen)
	blank := make([]byte, panel.ReportLen)
	panel.FrameInto(blank, nil, panel.OpBlank)

	display := o.display()
	send := func() error {
		r := readings(s, o)
		panel.ValuesInto(values, r, display)
		panel.FrameInto(frame, values, panel.OpRefresh)
		if err := dev.Send(frame); err != nil {
			return err
		}
		log.Debug("frame sent", "cpu_c", r.CPUTemp, "gpu_c", r.GPUTemp,
			"cpu_w", r.CPUPower, "fan_rpm", r.FanRPM)
		return nil
	}

	if err := send(); err != nil {
		return err
	}

	ticker := time.NewTicker(o.Refresh)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down, blanking panel")
			return dev.Send(blank)
		case <-ticker.C:
			if err := send(); err != nil {
				return err
			}
		}
	}
}

// runOnce sends a single frame and leaves it on screen.
func runOnce(cmd *cobra.Command, o Options) error {
	if err := o.validate(); err != nil {
		return fmt.Errorf("%w: %w", errConfig, err)
	}
	log := o.logger()

	s := telemetry.New(o.Paths)

	dev, err := open(o)
	if err != nil {
		return err
	}
	defer dev.Close()

	s.Prime()

	if err := dev.Send(panel.Frame(panel.Values(readings(s, o), o.display()), panel.OpRefresh)); err != nil {
		return err
	}
	log.Info("sent one frame", "node", dev.Path())
	return nil
}

// overlayFlag restores a flag the user set explicitly, so it wins over the
// value the config file supplied.
func overlayFlag(dst *Options, fromFlags Options, name string) {
	switch name {
	case "unit":
		dst.Unit = fromFlags.Unit
	case "show":
		dst.Mode = fromFlags.Mode
	case "cycle":
		dst.Cycle = fromFlags.Cycle
	case "only":
		dst.Target = fromFlags.Target
	case "freq":
		dst.Freq = fromFlags.Freq
	case "device":
		dst.Device = fromFlags.Device
	case "refresh":
		dst.Refresh = fromFlags.Refresh
	case "verbose":
		dst.Verbose = fromFlags.Verbose
	case "log-json":
		dst.JSON = fromFlags.JSON
	case "cpu-temp":
		dst.Paths.CPUTemp = fromFlags.Paths.CPUTemp
	case "gpu-hwmon":
		dst.Paths.GPU = fromFlags.Paths.GPU
	case "fan-path":
		dst.Paths.Fan = fromFlags.Paths.Fan
	case "pump-path":
		dst.Paths.Pump = fromFlags.Paths.Pump
	case "rapl":
		dst.Paths.RAPL = fromFlags.Paths.RAPL
	}
}
