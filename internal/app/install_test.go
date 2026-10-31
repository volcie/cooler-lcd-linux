package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/volcie/cooler-lcd-linux/internal/assets"
)

// The embedded rule must carry the group placeholder the renderer replaces.
// If someone edits the rule's GROUP=, install silently writes the wrong group.
func TestRenderRuleSubstitutesGroup(t *testing.T) {
	got := renderRule("volcie")

	if strings.Contains(got, assets.GroupToken) {
		t.Errorf("placeholder %s still present", assets.GroupToken)
	}
	if !strings.Contains(got, `GROUP="volcie"`) {
		t.Errorf("rendered rule missing the group:\n%s", got)
	}
	// Everything else must survive verbatim.
	if !strings.Contains(got, `ATTRS{idVendor}=="5131"`) ||
		!strings.Contains(got, `ATTRS{idProduct}=="2007"`) {
		t.Error("rendered rule lost the USB match")
	}
	if !strings.Contains(got, "MODE=\"0660\"") {
		t.Error("rendered rule lost the mode")
	}
}

func TestRenderUnitSubstitutesBinary(t *testing.T) {
	got := renderUnit("/usr/local/bin/cooler-lcd")

	if strings.Contains(got, assets.BinToken) {
		t.Errorf("placeholder %s still present", assets.BinToken)
	}
	if !strings.Contains(got, "ExecStart=/usr/local/bin/cooler-lcd") {
		t.Errorf("rendered unit missing ExecStart:\n%s", got)
	}
	if !strings.Contains(got, "WantedBy=default.target") {
		t.Error("rendered unit lost the install target")
	}
}

// writeIfChanged must not rewrite an identical file, so re-running install is
// idempotent and does not needlessly restart udev.
func TestWriteIfChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.conf")

	changed, err := writeIfChanged(path, "content", 0o644)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !changed {
		t.Error("first write reported no change")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "content" {
		t.Errorf("content = %q, want %q", data, "content")
	}

	changed, err = writeIfChanged(path, "content", 0o644)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Error("identical content reported a change")
	}

	changed, err = writeIfChanged(path, "different", 0o644)
	if err != nil {
		t.Fatalf("third write: %v", err)
	}
	if !changed {
		t.Error("changed content reported no change")
	}
}

func TestInstallDirsHonoursOverrides(t *testing.T) {
	t.Setenv("COOLER_LCD_BINDIR", "/tmp/b")
	t.Setenv("COOLER_LCD_RULEDIR", "/tmp/r")

	binDir, ruleDir := installDirs()
	if binDir != "/tmp/b" || ruleDir != "/tmp/r" {
		t.Errorf("installDirs() = %q, %q; want /tmp/b, /tmp/r", binDir, ruleDir)
	}
}

func TestElevatorIsOnPath(t *testing.T) {
	got := elevator()
	if got == "" {
		t.Fatal("no elevator chosen")
	}
	if _, err := exec.LookPath(got); err != nil {
		t.Errorf("chosen elevator %q is not on PATH", got)
	}
}

// A config file supplies defaults, and an explicit flag must win over it.
func TestConfigFilePrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
unit = "f"
show = "cycle"
cycle = "7s"
only = "gpu"
refresh = "500ms"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	opts := Options{
		Unit:    Celsius,
		Mode:    ShowTemps,
		Cycle:   2 * time.Second,
		Target:  ShowBoth,
		Freq:    FreqMax,
		Refresh: 200 * time.Millisecond,
	}
	if err := opts.applyConfig(cfg); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	if opts.Unit != Fahrenheit {
		t.Errorf("unit = %v, want fahrenheit", opts.Unit)
	}
	if opts.Mode != ShowCycle {
		t.Errorf("mode = %v, want cycle", opts.Mode)
	}
	if opts.Cycle != 7*time.Second {
		t.Errorf("cycle = %v, want 7s", opts.Cycle)
	}
	if opts.Target != ShowGPU {
		t.Errorf("target = %v, want gpu", opts.Target)
	}
	if opts.Refresh != 500*time.Millisecond {
		t.Errorf("refresh = %v, want 500ms", opts.Refresh)
	}

	// A flag the user set explicitly is restored over the file's value.
	fromFlags := opts
	fromFlags.Unit = Celsius
	overlayFlag(&opts, fromFlags, "unit")
	if opts.Unit != Celsius {
		t.Errorf("flag did not override config: unit = %v", opts.Unit)
	}
}

func TestConfigFileUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bogus = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should name the key: %v", err)
	}
}

// A missing default config is fine; a missing --config is a mistake.
func TestConfigFileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := loadConfig(""); err != nil {
		t.Errorf("missing default config should not error: %v", err)
	}
	if _, err := loadConfig(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Error("missing explicit config should error")
	}
}

func TestConfigTemplateParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(defaultConfigTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("the shipped template must parse: %v", err)
	}
	if cfg.Unit != "c" || cfg.Show != "temp" {
		t.Errorf("template defaults = %q/%q", cfg.Unit, cfg.Show)
	}
}

// systemd must be able to tell a permanent failure from a transient one:
// exit 2 stops retries, exit 1 allows them.
func TestExitCodeClassification(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		permanent bool
	}{
		{"nil", nil, false},
		{"config", fmt.Errorf("%w: bad key", errConfig), true},
		{"not installed", fmt.Errorf("%w: no unit", errNotInstalled), true},
		{"wrapped config", fmt.Errorf("outer: %w", fmt.Errorf("%w: x", errConfig)), true},
		{"transient", errors.New("device unplugged"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPermanent(c.err); got != c.permanent {
				t.Errorf("isPermanent = %v, want %v", got, c.permanent)
			}
		})
	}
}

// An explicit sensor path that does not exist must be rejected: silently
// reporting 0 would show "0C" on the panel with no indication of the problem.
func TestValidateRejectsMissingSensorPaths(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")

	base := Options{Refresh: time.Second, Unit: Celsius, Mode: ShowTemps,
		Cycle: 2 * time.Second, Target: ShowBoth, Freq: FreqMax}

	ok := base
	if err := ok.validate(); err != nil {
		t.Fatalf("defaults should validate: %v", err)
	}

	for name, mutate := range map[string]func(*Options){
		"cpu-temp":  func(o *Options) { o.Paths.CPUTemp = missing },
		"fan-path":  func(o *Options) { o.Paths.Fan = missing },
		"pump-path": func(o *Options) { o.Paths.Pump = missing },
		"rapl":      func(o *Options) { o.Paths.RAPL = missing },
		"gpu-hwmon": func(o *Options) { o.Paths.GPU = missing },
	} {
		t.Run(name, func(t *testing.T) {
			o := base
			mutate(&o)
			if err := o.validate(); err == nil {
				t.Errorf("%s: expected an error for a missing path", name)
			}
		})
	}

	// A gpu_hwmon that exists but is a file, not a directory.
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := base
	o.Paths.GPU = file
	if err := o.validate(); err == nil {
		t.Error("expected an error for a non-directory gpu-hwmon")
	}
}
