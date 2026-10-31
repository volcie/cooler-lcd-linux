package app

import (
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
)

// Paths touched by install. Overridable so tests can run unprivileged.
const (
	defaultBinDir  = "/usr/local/bin"
	defaultRuleDir = "/etc/udev/rules.d"
	ruleFileName   = "70-cooler-lcd.rules"
	unitFileName   = "cooler-lcd.service"
)

// installDirs resolves the destinations, honouring overrides.
func installDirs() (binDir, ruleDir string) {
	binDir = envOr("COOLER_LCD_BINDIR", defaultBinDir)
	ruleDir = envOr("COOLER_LCD_RULEDIR", defaultRuleDir)
	return binDir, ruleDir
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// primaryGroup returns the user's primary group name. Using it rather than a
// fixed "users" makes the udev rule work regardless of distro group layout.
func primaryGroup() (string, error) {
	out, err := exec.Command("id", "-gn").Output()
	if err != nil {
		return "", fmt.Errorf("detect primary group: %w", err)
	}
	g := strings.TrimSpace(string(out))
	if g == "" {
		return "", errors.New("empty primary group")
	}
	return g, nil
}

// renderRule substitutes the group into the embedded udev rule.
func renderRule(group string) string {
	return strings.Replace(assets.UdevRule, assets.GroupToken, fmt.Sprintf(`GROUP=%q`, group), 1)
}

// renderUnit substitutes the binary path into the embedded unit.
func renderUnit(binPath string) string {
	return strings.ReplaceAll(assets.ServiceUnit, assets.BinToken, binPath)
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

	binDir, ruleDir := installDirs()
	binPath := filepath.Join(binDir, "cooler-lcd")
	rulePath := filepath.Join(ruleDir, ruleFileName)

	say("cooler-lcd install")
	say("")

	// 1. Binary.
	say("1/4  binary")
	if self == binPath {
		say("     already running from %s", binPath)
	} else if changed, err := installFile(self, binPath, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	} else if changed {
		say("     installed %s", binPath)
	} else {
		say("     %s already current", binPath)
	}

	// 2. Udev rule.
	say("2/4  device access")
	group, err := primaryGroup()
	if err != nil {
		return err
	}
	rulePath2 := rulePath
	say("     group: %s", group)

	changed, err := writeIfChanged(rulePath, renderRule(group), 0o644)
	if err != nil {
		return fmt.Errorf("install udev rule: %w", err)
	}
	if changed {
		say("     installed %s", rulePath2)
		// The rule is on disk at this point, so a failure to reload is a
		// warning rather than a fatal error: the user can reload later and the
		// rest of the install is still valid.
		if err := runElevated("udevadm", "control", "--reload-rules"); err != nil {
			warn(out, "could not reload udev rules: %v", err)
			warn(out, "run later: sudo udevadm control --reload-rules && sudo udevadm trigger --subsystem-match=hidraw")
		} else if err := runElevated("udevadm", "trigger", "--subsystem-match=hidraw"); err != nil {
			warn(out, "could not trigger udev: %v", err)
		}
	} else {
		say("     rule already current")
	}

	// 3. User service. This must run as the invoking user: a root-run
	// "systemctl --user" would target root's session, not yours.
	say("3/4  user service")
	if noService {
		say("     skipped (--no-service)")
	} else if err := installService(binPath, noStart); err != nil {
		return fmt.Errorf("install service: %w", err)
	} else if noStart {
		say("     installed, not started (--no-start)")
	} else {
		say("     enabled and started")
	}

	// 4. Report.
	say("4/4  status")
	havePanel := false
	if devices, err := panel.Enumerate(); err == nil && len(devices) > 0 {
		havePanel = true
		say("     found %s", devices[0].Path)
		if !probeWritable(devices[0].Path) {
			say("")
			say("     NOTE: %s is not writable yet.", devices[0].Path)
			say("     Unplug and replug the cooler's USB header.")
		}
	} else {
		say("     no 5131:2007 device detected yet")
	}

	// Only point at manual commands when the service is not already doing the
	// job; telling someone to run the streamer right after starting it is
	// misleading.
	switch {
	case noService:
		say("")
		say("The service was skipped, so nothing is driving the panel.")
		say("  cooler-lcd                 stream to the panel (Ctrl-C blanks it)")
		say("  cooler-lcd install         install the service instead")
	case noStart:
		say("")
		say("Service installed but not started.")
		say("  cooler-lcd start")
	case havePanel:
		say("")
		say("The service is running and driving the panel now.")
		say("  journalctl --user -u cooler-lcd -f    watch it")
		say("  cooler-lcd print                      check the sensor mapping")
	default:
		say("")
		say("The service is waiting for the panel; it starts as soon as the")
		say("cooler is plugged in. No further action needed.")
	}
	return nil
}

// installService writes and enables the systemd user unit. It never runs as
// root: the caller is the user whose session owns the unit.
func installService(binPath string, noStart bool) error {
	unitDir := filepath.Join(xdgConfigHome(), "systemd", "user")
	unitPath := filepath.Join(unitDir, unitFileName)

	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", unitDir, err)
	}
	if _, err := writeIfChanged(unitPath, renderUnit(binPath), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	if err := exec.Command("systemctl", "--user", "enable", "cooler-lcd.service").Run(); err != nil {
		return fmt.Errorf("systemctl enable: %w", err)
	}
	if noStart {
		return nil
	}
	// "enable --now" restarts an already-running unit. Start only when it is
	// not already active, so re-running install does not interrupt the panel.
	if exec.Command("systemctl", "--user", "is-active", "--quiet", "cooler-lcd.service").Run() == nil {
		return nil
	}
	if err := exec.Command("systemctl", "--user", "start", "cooler-lcd.service").Run(); err != nil {
		return fmt.Errorf("systemctl start: %w", err)
	}
	return nil
}

func xdgConfigHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config")
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
	var keepBinary bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the udev rule, service and installed binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			binDir, ruleDir := installDirs()
			binPath := filepath.Join(binDir, "cooler-lcd")
			rulePath := filepath.Join(ruleDir, ruleFileName)
			unitPath := filepath.Join(xdgConfigHome(), "systemd", "user", unitFileName)

			fmt.Fprintln(out, "cooler-lcd uninstall")

			// Service first, as the user: a root-run systemctl --user would
			// address root's session instead.
			if err := exec.Command("systemctl", "--user", "disable", "--now", "cooler-lcd.service").Run(); err == nil {
				fmt.Fprintln(out, "     service stopped")
			}
			if err := os.Remove(unitPath); err == nil {
				fmt.Fprintln(out, "     removed", unitPath)
			}
			_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

			if err := removeElevated(rulePath); err == nil {
				fmt.Fprintln(out, "     removed", rulePath)
				_ = runElevated("udevadm", "control", "--reload-rules")
			}

			if !keepBinary {
				if err := removeElevated(binPath); err == nil {
					fmt.Fprintln(out, "     removed", binPath)
				}
			}

			fmt.Fprintln(out, "Done. Replug the cooler to restore default permissions.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&keepBinary, "keep-binary", false, "leave the installed binary in place")
	return cmd
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
