package telemetry

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// A pinned path must be used verbatim, and malformed content must degrade to
// zero rather than error out mid-stream.
func TestCPUTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "temp1_input")
	writeFile(t, p, "56190\n")

	s := &Sampler{paths: Paths{CPUTemp: p}}
	if got := s.CPUTemp(); got != 56.19 {
		t.Errorf("CPUTemp = %v, want 56.19", got)
	}

	bad := filepath.Join(dir, "bad")
	writeFile(t, bad, "not-a-number\n")
	s = &Sampler{paths: Paths{CPUTemp: bad}}
	if got := s.CPUTemp(); got != 0 {
		t.Errorf("CPUTemp with junk = %v, want 0", got)
	}

	s = &Sampler{paths: Paths{CPUTemp: filepath.Join(dir, "missing")}}
	if got := s.CPUTemp(); got != 0 {
		t.Errorf("CPUTemp with missing file = %v, want 0", got)
	}
}

func TestGPUMetrics(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "temp1_input"), "33000\n")
	writeFile(t, filepath.Join(dir, "power1_average"), "39000000\n")
	writeFile(t, filepath.Join(dir, "freq1_input"), "1492000000\n")
	writeFile(t, filepath.Join(dir, "device", "gpu_busy_percent"), "18\n")

	s := &Sampler{paths: Paths{GPU: dir}}
	if got := s.GPUTemp(); got != 33 {
		t.Errorf("GPUTemp = %v, want 33", got)
	}
	if got := s.GPUPower(); got != 39 {
		t.Errorf("GPUPower = %v, want 39", got)
	}
	if got := s.GPUFreqMHz(); got != 1492 {
		t.Errorf("GPUFreqMHz = %v, want 1492", got)
	}
	if got := s.GPUUsage(); got != 18 {
		t.Errorf("GPUUsage = %v, want 18", got)
	}
}

func TestFanAndPump(t *testing.T) {
	dir := t.TempDir()
	fan := filepath.Join(dir, "fan1_input")
	pump := filepath.Join(dir, "fan2_input")
	writeFile(t, fan, "897\n")
	writeFile(t, pump, "2100\n")

	s := &Sampler{paths: Paths{Fan: fan, Pump: pump}}
	if got := s.FanRPM(); got != 897 {
		t.Errorf("FanRPM = %v, want 897", got)
	}
	if got := s.PumpRPM(); got != 2100 {
		t.Errorf("PumpRPM = %v, want 2100", got)
	}

	// No pump configured is normal on an air cooler.
	s = &Sampler{paths: Paths{Fan: fan}}
	if got := s.PumpRPM(); got != 0 {
		t.Errorf("PumpRPM unset = %v, want 0", got)
	}
}

// Wattage comes from an energy counter delta, so equal readings must yield zero
// and a reset counter must not produce a negative or absurd value.
func TestCPUPowerDelta(t *testing.T) {
	dir := t.TempDir()
	rapl := filepath.Join(dir, "energy_uj")
	writeFile(t, rapl, "1000000\n")

	clock := &fakeClock{}
	s := &Sampler{
		paths:    Paths{RAPL: rapl},
		raplPrev: 1000000,
		raplAt:   clock.now(),
		clock:    clock.now,
	}

	// 9,000,000 uJ over 0.5 s = 18 W.
	writeFile(t, rapl, "10000000\n")
	clock.advance(0.5)
	if got := s.CPUPower(); got != 18 {
		t.Errorf("CPUPower = %v, want 18", got)
	}

	// Counter reset: no spike, no negative.
	writeFile(t, rapl, "5\n")
	clock.advance(0.5)
	if got := s.CPUPower(); got != 0 {
		t.Errorf("CPUPower after reset = %v, want 0", got)
	}
}

func TestCPUPowerWithoutRAPL(t *testing.T) {
	s := &Sampler{paths: Paths{RAPL: filepath.Join(t.TempDir(), "absent")}}
	if got := s.CPUPower(); got != 0 {
		t.Errorf("CPUPower without RAPL = %v, want 0", got)
	}
}

// fakeClock drives time-dependent sampling deterministically.
type fakeClock struct{ base float64 }

func (c *fakeClock) now() time.Time {
	if c.base == 0 {
		c.base = 1e9
	}
	sec := int64(c.base)
	nsec := int64((c.base - float64(sec)) * 1e9)
	return time.Unix(sec, nsec)
}

func (c *fakeClock) advance(seconds float64) { c.base += seconds }

// A truncated /proc/meminfo must not be reported as 100% usage: MemTotal present
// with MemAvailable missing would otherwise look like a full disk of RAM.
func TestRAMPercentRejectsPartialInput(t *testing.T) {
	cases := []struct {
		name    string
		content string
		ok      bool
		want    float64
	}{
		{"healthy", "MemTotal:       32000000 kB\nMemAvailable:   16000000 kB\n", true, 50},
		{"missing available", "MemTotal:       32000000 kB\n", false, 0},
		{"missing total", "MemAvailable:   16000000 kB\n", false, 0},
		{"empty", "", false, 0},
		{"available exceeds total", "MemTotal: 100 kB\nMemAvailable: 200 kB\n", false, 0},
		{"non-numeric", "MemTotal: abc kB\nMemAvailable: 100 kB\n", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meminfo")
			writeFile(t, path, c.content)

			pct, ok := parseMeminfo(func() (io.ReadCloser, error) {
				return os.Open(path)
			})
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && (pct < c.want-0.01 || pct > c.want+0.01) {
				t.Errorf("pct = %v, want %v", pct, c.want)
			}
		})
	}
}
