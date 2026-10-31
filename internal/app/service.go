package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/volcie/cooler-lcd-linux/internal/panel"
)

const serviceUnit = "cooler-lcd.service"

// newServiceCmd groups the service lifecycle commands so the user never has to
// type `systemctl --user`.
func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service",
		Short:   "Control the background service",
		Long:    "Control the cooler-lcd user service. Shorthands: status, start, stop, restart.",
		Aliases: []string{"svc"},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "status",
			Short: "Show whether the service is running and what it reports",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return serviceStatus(cmd)
			},
		},
		&cobra.Command{
			Use:   "start",
			Short: "Start the service",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return serviceAction(cmd, "start")
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the service",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return serviceAction(cmd, "stop")
			},
		},
		&cobra.Command{
			Use:   "restart",
			Short: "Restart the service (picks up config changes)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return serviceAction(cmd, "restart")
			},
		},
	)
	return cmd
}

// serviceStatus reports the unit state plus the two things a user actually
// cares about: is the panel visible, and is it writable.
func serviceStatus(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	active := systemctlQuiet("is-active", serviceUnit)
	enabled := systemctlQuiet("is-enabled", serviceUnit)
	state := "stopped"
	if active {
		state = "running"
	}
	unitState := "disabled"
	if enabled {
		unitState = "enabled"
	}

	fmt.Fprintf(out, "service  %s (%s)\n", state, unitState)

	if devices, err := panel.Enumerate(); err == nil && len(devices) > 0 {
		node := devices[0].Path
		writable := probeWritable(node)
		fmt.Fprintf(out, "panel    %s", node)
		if writable {
			fmt.Fprintln(out, " (writable)")
		} else {
			fmt.Fprintln(out, " (NOT writable - replug the cooler)")
		}
	} else {
		fmt.Fprintln(out, "panel    not detected")
	}

	// Surface the last few log lines so a stopped service explains itself
	// without a separate journalctl invocation.
	if logs := recentLogs(6); logs != "" {
		fmt.Fprintln(out, "\nrecent log:")
		fmt.Fprintln(out, logs)
	}
	if !active {
		fmt.Fprintln(out, "\nstart it with: cooler-lcd start")
	}
	return nil
}

func serviceAction(cmd *cobra.Command, action string) error {
	out := cmd.OutOrStdout()

	// Fail clearly when the unit is not installed: a restart would not help.
	if action != "stop" && !unitInstalled() {
		return fmt.Errorf("%w: run 'cooler-lcd install' first", errNotInstalled)
	}

	was := systemctlQuiet("is-active", serviceUnit)

	switch action {
	case "start":
		// Report what happened, not what was asked for: starting an already
		// running service is a no-op, and saying "started" would be a lie.
		if was {
			fmt.Fprintln(out, "already running")
			return nil
		}
	case "stop":
		if !was {
			fmt.Fprintln(out, "already stopped")
			return nil
		}
	}

	if err := runElevated("systemctl", action, serviceUnit); err != nil {
		return fmt.Errorf("systemctl %s: %w", action, err)
	}

	now := systemctlQuiet("is-active", serviceUnit)
	switch action {
	case "stop":
		if now {
			fmt.Fprintln(out, "stop requested, still running; check: cooler-lcd status")
		} else {
			fmt.Fprintln(out, "stopped")
		}
	default:
		if now {
			fmt.Fprintln(out, actionVerb(action))
		} else {
			fmt.Fprintf(out, "%s, but it is not running; check: cooler-lcd status\n", actionVerb(action))
		}
	}
	return nil
}

func actionVerb(action string) string {
	switch action {
	case "start":
		return "started"
	case "restart":
		return "restarted"
	default:
		return action
	}
}

// systemctlQuiet reports whether a systemctl query succeeded. Used for
// read-only queries, which do not need elevation.
func systemctlQuiet(args ...string) bool {
	return exec.Command("systemctl", args...).Run() == nil
}

// recentLogs returns the tail of the unit's journal, or "" if unavailable.
func recentLogs(n int) string {
	out, err := exec.Command("journalctl", "-u", serviceUnit,
		"-n", fmt.Sprint(n), "--no-pager", "-o", "cat", "--quiet").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

// unitInstalled reports whether the system service unit exists.
func unitInstalled() bool {
	_, _, unitDir := installDirs()
	_, err := os.Stat(filepath.Join(unitDir, unitFileName))
	return err == nil
}
