package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/spf13/cobra"
	"github.com/volcie/cooler-lcd-linux/internal/assets"
)

// Config mirrors the flags in a file, so the service can be configured without
// editing the systemd unit. Command-line flags still win: the file supplies
// defaults, an explicit flag overrides them.
//
// Every field is optional; omitting one means "use the built-in default".
type Config struct {
	// Display.
	Unit  string `toml:"unit"`  // "c" or "f"
	Show  string `toml:"show"`  // "temp", "usage" or "cycle"
	Cycle string `toml:"cycle"` // duration, e.g. "5s"
	Only  string `toml:"only"`  // "both", "cpu" or "gpu"
	Freq  string `toml:"freq"`  // "max" or "avg"

	// Runtime.
	Device  string `toml:"device"`
	Refresh string `toml:"refresh"` // duration, e.g. "200ms"
	Verbose bool   `toml:"verbose"`
	LogJSON bool   `toml:"log_json"`

	// Sensor overrides.
	CPUTemp string `toml:"cpu_temp"`
	GPU     string `toml:"gpu_hwmon"`
	Fan     string `toml:"fan_path"`
	Pump    string `toml:"pump_path"`
	RAPL    string `toml:"rapl"`
}

// configPath returns the file to read.
//
// The service is a system unit, so its configuration is system-wide. As root
// there is exactly one answer. As a normal user, /etc is readable but not
// writable, so a per-user file is also honoured: that is what makes
// "cooler-lcd print" and a foreground run usable without sudo.
//
// Order: --config, then $COOLER_LCD_CONFIG, then, for a non-root user, the
// per-user file if it exists, then the system file.
func configPath(explicit string) (string, bool) {
	if explicit != "" {
		return explicit, true
	}
	if p := os.Getenv("COOLER_LCD_CONFIG"); p != "" {
		return p, false
	}
	if os.Geteuid() != 0 {
		if home, err := os.UserHomeDir(); err == nil {
			userPath := filepath.Join(home, ".config", "cooler-lcd", "config.toml")
			if _, err := os.Stat(userPath); err == nil {
				return userPath, false
			}
		}
	}
	return assets.SystemConfigPath, false
}

// loadConfig reads the config file. A missing default file is not an error;
// a missing file named explicitly on the command line is.
func loadConfig(explicit string) (Config, error) {
	var cfg Config

	path, mustExist := configPath(explicit)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !mustExist {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return cfg, fmt.Errorf("parse %s: unknown key %s", path, unknownKeys(strict))
		}
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// unknownKeys lists the offending keys from a strict-mode decode error.
func unknownKeys(err *toml.StrictMissingError) string {
	var keys []string
	for i := range err.Errors {
		if k := err.Errors[i].Key(); len(k) > 0 {
			keys = append(keys, strings.Join(k, "."))
		}
	}
	if len(keys) == 0 {
		return "(unnamed)"
	}
	return strings.Join(keys, ", ")
}

// parseDuration parses a TOML duration string, naming the field on failure.
func parseDuration(field, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("config %s: %q is not a duration (try \"5s\", \"200ms\")", field, value)
	}
	return d, nil
}

// applyConfig fills unset options from the file. Values already set on the
// command line are left alone, which is why each check is against the default
// rather than a separate "was set" flag.
func (o *Options) applyConfig(c Config) error {
	if c.Unit != "" {
		if err := o.Unit.Set(c.Unit); err != nil {
			return fmt.Errorf("config unit: %w", err)
		}
	}
	if c.Show != "" {
		if err := o.Mode.Set(c.Show); err != nil {
			return fmt.Errorf("config show: %w", err)
		}
	}
	if c.Cycle != "" {
		d, err := parseDuration("cycle", c.Cycle)
		if err != nil {
			return err
		}
		o.Cycle = d
	}
	if c.Only != "" {
		if err := o.Target.Set(c.Only); err != nil {
			return fmt.Errorf("config only: %w", err)
		}
	}
	if c.Freq != "" {
		if err := o.Freq.Set(c.Freq); err != nil {
			return fmt.Errorf("config freq: %w", err)
		}
	}
	if c.Device != "" {
		o.Device = c.Device
	}
	if c.Refresh != "" {
		d, err := parseDuration("refresh", c.Refresh)
		if err != nil {
			return err
		}
		o.Refresh = d
	}
	if c.Verbose {
		o.Verbose = true
	}
	if c.LogJSON {
		o.JSON = true
	}
	if c.CPUTemp != "" {
		o.Paths.CPUTemp = c.CPUTemp
	}
	if c.GPU != "" {
		o.Paths.GPU = c.GPU
	}
	if c.Fan != "" {
		o.Paths.Fan = c.Fan
	}
	if c.Pump != "" {
		o.Paths.Pump = c.Pump
	}
	if c.RAPL != "" {
		o.Paths.RAPL = c.RAPL
	}
	return nil
}

// defaultConfigTemplate is written by "cooler-lcd config init".
const defaultConfigTemplate = `# cooler-lcd configuration.
#
# Flags override anything set here, so this file only needs what you want to
# differ from the defaults.

# What the panel shows.
unit  = "c"        # "c" or "f"
show  = "temp"     # "temp", "usage" or "cycle"
cycle = "2s"       # seconds per mode when show = "cycle"
only  = "both"     # "both", "cpu" or "gpu"
freq  = "max"      # "max" or "avg" CPU clock

# Runtime.
refresh  = "200ms"
# device = "/dev/hidraw2"   # default: autodetect by USB ID
verbose  = false
log_json = false

# Sensor overrides. Leave unset to autodetect; run "cooler-lcd print" to see
# what resolved.
# cpu_temp  = "/sys/class/hwmon/hwmon4/temp1_input"
# gpu_hwmon = "/sys/class/hwmon/hwmon2"
# fan_path  = "/sys/class/hwmon/hwmon2/fan1_input"
# pump_path = "/sys/class/hwmon/hwmon0/fan2_input"
# rapl      = "/sys/class/powercap/intel-rapl:0/energy_uj"
`

func newConfigCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, create or validate the configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			path, _ := configPath(configFile)
			cfg, err := loadConfig(configFile)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "file: %s\n", path)
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				fmt.Fprintln(out, "status: not present (defaults in use)")
				fmt.Fprintf(out, "\ncreate it with: cooler-lcd config init\n")
				return nil
			}
			fmt.Fprintln(out, "status: loaded")
			body, err := toml.Marshal(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\n%s", body)
			return nil
		},
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a commented default configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := configPath(configFile)
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists; edit it, or remove it first", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, []byte(defaultConfigTemplate), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			fmt.Fprintln(cmd.OutOrStdout(), "apply it with: cooler-lcd restart")
			return nil
		},
	}

	cmd.AddCommand(initCmd)
	return cmd
}
