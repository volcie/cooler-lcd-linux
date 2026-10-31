package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/volcie/cooler-lcd-linux/internal/assets"
	"github.com/volcie/cooler-lcd-linux/internal/panel"
	"github.com/volcie/cooler-lcd-linux/internal/version"
)

// Paths touched by install. Overridable so tests can run unprivileged.
const (
	defaultBinDir  = "/usr/local/bin"
	defaultRuleDir = "/etc/udev/rules.d"
	defaultUnitDir = "/etc/systemd/system"
	ruleFileName   = "70-cooler-lcd.rules"
	unitFileName   = "cooler-lcd.service"
)

// installDirs resolves the destinations, honouring overrides.
func installDirs() (binDir, ruleDir, unitDir string) {
	return envOr("COOLER_LCD_BINDIR", defaultBinDir),
		envOr("COOLER_LCD_RULEDIR", defaultRuleDir),
		envOr("COOLER_LCD_UNITDIR", defaultUnitDir)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ensureServiceGroup creates the dedicated group that owns the panel device.
//
// A fixed group is required rather than a login user's: the service runs with
// DynamicUser=yes, so it has no home and no primary group of its own, and the
// udev rule cannot point at whichever user happens to be logged in. Running
// the old code under sudo also detected "root" as the group, which would have
// left the unprivileged service unable to open the device.
func ensureServiceGroup(name string) error {
	if exec.Command("getent", "group", name).Run() == nil {
		return nil
	}
	return runElevated("groupadd", "--system", name)
}

// renderRule substitutes the service group into the embedded udev rule.
func renderRule(group string) string {
	return strings.ReplaceAll(assets.UdevRule, assets.GroupToken, group)
}

// renderUnit substitutes the binary path and group into the embedded unit.
func renderUnit(binPath, group string) string {
	s := strings.ReplaceAll(assets.ServiceUnit, assets.BinToken, binPath)
	return strings.ReplaceAll(s, assets.GroupToken, group)
}

func newInstallCmd(opts *Options) *cobra.Command {
	var (
		noService bool
		noStart   bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the binary, udev rule and systemd service",
		Long: `Install cooler-lcd on this machine.

Copies the running binary to ` + defaultBinDir + `, installs a udev rule granting
access to the panel, and enables a systemd user service. Elevation is requested
only for the two system-wide files.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstall(cmd, *opts, noService, noStart)
		},
	}
	cmd.Flags().BoolVar(&noService, "no-service", false, "skip the systemd user service")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "install the service but do not start it")
	return cmd
}

// runInstall performs the install. It is deliberately split so that the parts
// needing root are the only ones elevated, and everything touching the user's
// systemd session runs as the original user.
func runInstall(cmd *cobra.Command, _ Options, noService, noStart bool) error {
	out := cmd.OutOrStdout()
	say := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...) }

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolve own binary: %w", err)
	}

	binDir, ruleDir, unitDir := installDirs()
	binPath := filepath.Join(binDir, "cooler-lcd")
	rulePath := filepath.Join(ruleDir, ruleFileName)
	unitPath := filepath.Join(unitDir, unitFileName)
	group := assets.ServiceGroup
	cfgPath := envOr("COOLER_LCD_CONFIG", assets.SystemConfigPath)

	// 1. Binary.
	say("1/4  binary")
	if self == binPath {
		// Re-running the installed binary is allowed (it picks up config and
		// rule changes), but the rendered rule and unit come from THIS build.
		// If the installed copy is older than the repo, the group and other
		// constants may differ, so say which version is doing the installing.
		say("     running the installed binary (%s)", version.String())
		say("     if you expected a newer one, rebuild and run dist/ instead")
	} else if changed, err := installFile(self, binPath, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	} else if changed {
		say("     installed %s", binPath)
	} else {
		say("     %s already current", binPath)
	}

	// 2. Group and udev rule. The service runs with DynamicUser=yes, which has
	// no home and no primary group, so the device is granted to a dedicated
	// system group rather than a login user's group.
	say("2/4  device access")
	if err := ensureServiceGroup(group); err != nil {
		return fmt.Errorf("create group %s: %w", group, err)
	}
	say("     group: %s", group)

	if changed, err := writeIfChanged(rulePath, renderRule(group), 0o644); err != nil {
		return fmt.Errorf("install udev rule: %w", err)
	} else if changed {
		say("     installed %s", rulePath)
		if err := runElevated("udevadm", "control", "--reload-rules"); err != nil {
			warn(out, "could not reload udev rules: %v", err)
			warn(out, "run later: sudo udevadm control --reload-rules && sudo udevadm trigger --subsystem-match=hidraw")
		} else if err := runElevated("udevadm", "trigger", "--subsystem-match=hidraw"); err != nil {
			warn(out, "could not trigger udev: %v", err)
		}
	} else {
		say("     rule already current")
	}

	// 3. System service.
	say("3/4  service")
	if noService {
		say("     skipped (--no-service)")
	} else if err := installService(unitPath, binPath, group, noStart); err != nil {
		return fmt.Errorf("install service: %w", err)
	} else if noStart {
		say("     installed, not started (--no-start)")
	} else if systemctlQuiet("is-active", serviceUnit) {
		say("     installed and running")
	} else {
		say("     installed but not running; check: cooler-lcd status")
	}

	// 4. Config.
	say("4/4  config")
	if fileExists(cfgPath) {
		say("     %s already present", cfgPath)
	} else {
		if _, err := writeIfChanged(cfgPath, defaultConfigTemplate, 0o644); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		say("     wrote %s (defaults)", cfgPath)
	}

	say("")
	if noService {
		say("The service was skipped, so nothing is driving the panel.")
		say("  cooler-lcd             stream in the foreground")
		say("  cooler-lcd install     install the service instead")
	} else if noStart {
		say("Start it with: cooler-lcd start")
	} else {
		say("Logs:    journalctl -u cooler-lcd -f")
		say("Config:  %s", cfgPath)
		say("Sensors: cooler-lcd print")
	}
	return nil
}

// installService writes and enables the system unit. It runs as root, so there
// is no "systemctl --user": the unit belongs to the machine, not a session.
func installService(unitPath, binPath, group string, noStart bool) error {
	if _, err := writeIfChanged(unitPath, renderUnit(binPath, group), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	if err := runElevated("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := runElevated("systemctl", "enable", serviceUnit); err != nil {
		return fmt.Errorf("enable: %w", err)
	}
	if noStart {
		return nil
	}
	// enable --now would restart a running unit; start only if inactive.
	if systemctlQuiet("is-active", serviceUnit) {
		return nil
	}
	return runElevated("systemctl", "start", serviceUnit)
}

// writeIfChanged writes content unless the destination already matches. It
// reports whether anything was written.
//
// Elevation is driven by capability, not by a flag: writing directly is tried
// first, and only a permission failure escalates. That keeps install usable
// unprivileged when the destinations are already writable.
func writeIfChanged(path, content string, mode os.FileMode) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && string(current) == content {
		return false, nil
	}
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		err = os.WriteFile(path, []byte(content), mode)
	}
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return true, err
	}
	return true, writeElevated(path, content, mode)
}

// installFile copies src to dst, escalating only if the direct copy is denied.
// An identical destination is left alone, so re-running install is a no-op.
func installFile(src, dst string, mode os.FileMode) (changed bool, err error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	if existing, err := os.ReadFile(dst); err == nil && bytes.Equal(existing, data) {
		return false, nil
	}
	err = os.MkdirAll(filepath.Dir(dst), 0o755)
	if err == nil {
		err = os.WriteFile(dst, data, mode)
	}
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return true, err
	}
	return true, runElevated("install", "-Dm"+fmt.Sprintf("%o", mode), src, dst)
}

// writeElevated writes a system file by piping content through sudo tee.
// Using tee avoids a temp file with the wrong ownership or mode.
func writeElevated(path, content string, mode os.FileMode) error {
	// Ensure the parent exists first; tee cannot create directories.
	if err := runElevated("mkdir", "-p", filepath.Dir(path)); err != nil {
		return err
	}
	c := exec.Command(elevator(), "tee", path)
	c.Stdin = strings.NewReader(content)
	c.Stdout = nil
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return err
	}
	return runElevated("chmod", fmt.Sprintf("%o", mode), path)
}

// runElevated runs a command as root, unless already root.
func runElevated(name string, args ...string) error {
	if os.Geteuid() == 0 {
		c := exec.Command(name, args...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c.Run()
	}
	c := exec.Command(elevator(), append([]string{name}, args...)...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

// elevator picks the available privilege tool. run0 is systemd's replacement
// for sudo and prompts through polkit without needing a tty password.
func elevator() string {
	for _, candidate := range []string{"sudo", "run0", "doas", "pkexec"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	return "sudo"
}

// probeWritable reports whether the panel node can be opened for writing.
func probeWritable(node string) bool {
	dev, err := panel.OpenPath(node)
	if err != nil {
		return false
	}
	dev.Close()
	return true
}

func newUninstallCmd() *cobra.Command {
	var keepBinary, keepConfig bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the udev rule, service and installed binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			binDir, ruleDir, unitDir := installDirs()
			binPath := filepath.Join(binDir, "cooler-lcd")
			rulePath := filepath.Join(ruleDir, ruleFileName)
			unitPath := filepath.Join(unitDir, unitFileName)

			// Report only real changes. Printing "removed X" for a file that was
			// not there, or a closing "Done." when nothing happened, makes the
			// output untrustworthy.
			changed := 0

			if unitInstalled() {
				if err := runElevated("systemctl", "disable", "--now", serviceUnit); err == nil {
					fmt.Fprintln(out, "stopped service")
				}
				if err := os.Remove(unitPath); err == nil {
					fmt.Fprintln(out, "removed "+unitPath)
					changed++
				}
				_ = runElevated("systemctl", "daemon-reload")
			}

			if fileExists(rulePath) {
				if err := removeElevated(rulePath); err == nil {
					fmt.Fprintln(out, "removed "+rulePath)
					changed++
					_ = runElevated("udevadm", "control", "--reload-rules")
				}
			}

			if !keepBinary && fileExists(binPath) {
				if err := removeElevated(binPath); err == nil {
					fmt.Fprintln(out, "removed "+binPath)
					changed++
				}
			}

			// The config is user data; default to keeping it.
			cfgPath := envOr("COOLER_LCD_CONFIG", assets.SystemConfigPath)
			if configFile != "" {
				cfgPath = configFile
			}
			if fileExists(cfgPath) {
				if !keepConfig && askDeleteConfig(cmd, cfgPath) {
					if err := removeElevated(cfgPath); err == nil {
						fmt.Fprintln(out, "removed "+cfgPath)
						changed++
						removeEmptyDir(filepath.Dir(cfgPath))
					}
				} else {
					fmt.Fprintln(out, "kept "+cfgPath)
				}
			}

			if changed == 0 {
				fmt.Fprintln(out, "nothing to remove")
				return nil
			}
			fmt.Fprintln(out, "removed. the device node keeps its current permissions until it is re-created or the cooler is reconnected")
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepBinary, "keep-binary", false, "leave the installed binary in place")
	cmd.Flags().BoolVar(&keepConfig, "keep-config", false, "keep the configuration file without asking")
	return cmd
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// askDeleteConfig prompts before deleting user data, defaulting to no. It
// refuses to prompt without a terminal: a piped or unattended uninstall must
// never hang waiting for input it will not get, and the safe default is to keep
// the file.
func askDeleteConfig(cmd *cobra.Command, path string) bool {
	in := cmd.InOrStdin()
	f, ok := in.(*os.File)
	if !ok || !termIsTerminal(f) {
		return false
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\nDelete the configuration file %s? [y/N] ", path)
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return false
	}
	return parseYes(sc.Text())
}

// parseYes accepts only an explicit affirmative; anything else, including empty
// input, means no.
func parseYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	}
	return false
}

// termIsTerminal reports whether f is a character device.
func termIsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// removeEmptyDir removes dir if it is empty, ignoring failures.
func removeEmptyDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}

// removeElevated deletes a file, escalating only when the direct removal is
// denied.
func removeElevated(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	err := os.Remove(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrPermission) {
		return err
	}
	return runElevated("rm", "-f", path)
}

// warn prints a non-fatal problem.
func warn(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "     warning: "+format+"\n", a...)
}
