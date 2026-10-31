package app

import (
	"testing"
	"time"

	"github.com/volcie/cooler-lcd-linux/internal/panel"
)

func TestEnumParsing(t *testing.T) {
	cases := []struct {
		name  string
		parse func(string) error
		input string
		ok    bool
	}{
		{"unit c", func(s string) error { var u TempUnit; return u.Set(s) }, "c", true},
		{"unit celsius", func(s string) error { var u TempUnit; return u.Set(s) }, "celsius", true},
		{"unit f", func(s string) error { var u TempUnit; return u.Set(s) }, "F", true},
		{"unit bad", func(s string) error { var u TempUnit; return u.Set(s) }, "kelvin", false},

		{"show temp", func(s string) error { var m DisplayMode; return m.Set(s) }, "temp", true},
		{"show usage", func(s string) error { var m DisplayMode; return m.Set(s) }, "usages", true},
		{"show cycle", func(s string) error { var m DisplayMode; return m.Set(s) }, "cycle", true},
		{"show bad", func(s string) error { var m DisplayMode; return m.Set(s) }, "0", false},

		{"only both", func(s string) error { var v ShowTarget; return v.Set(s) }, "both", true},
		{"only cpu", func(s string) error { var v ShowTarget; return v.Set(s) }, "cpu", true},
		{"only gpu", func(s string) error { var v ShowTarget; return v.Set(s) }, "gpu", true},
		{"only bad", func(s string) error { var v ShowTarget; return v.Set(s) }, "1", false},

		{"freq max", func(s string) error { var f FreqMode; return f.Set(s) }, "max", true},
		{"freq avg", func(s string) error { var f FreqMode; return f.Set(s) }, "average", true},
		{"freq bad", func(s string) error { var f FreqMode; return f.Set(s) }, "median", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.parse(c.input)
			if c.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// The words the user types must land on the documented protocol values.
func TestEnumWireMapping(t *testing.T) {
	modes := map[DisplayMode]int{
		ShowTemps:  panel.ModeTemps,
		ShowUsages: panel.ModeUsages,
		ShowCycle:  panel.ModeCycle,
	}
	for m, want := range modes {
		if got := m.wire(); got != want {
			t.Errorf("%q maps to %d, want %d", m, got, want)
		}
	}

	targets := map[ShowTarget]int{
		ShowBoth: panel.GPUBoth,
		ShowCPU:  panel.CPUOnly,
		ShowGPU:  panel.GPUOnly,
	}
	for v, want := range targets {
		if got := v.wire(); got != want {
			t.Errorf("%q maps to %d, want %d", v, got, want)
		}
	}
}

func TestCycleSeconds(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
		ok   bool
	}{
		{2 * time.Second, 2, true},
		{0, 0, true},
		{255 * time.Second, 255, true},
		{256 * time.Second, 0, false},
		{1500 * time.Millisecond, 0, false}, // sub-second cannot be encoded
	}
	for _, c := range cases {
		got, err := cycleSeconds(c.in)
		if c.ok && err != nil {
			t.Errorf("cycleSeconds(%v): unexpected error %v", c.in, err)
			continue
		}
		if !c.ok {
			if err == nil {
				t.Errorf("cycleSeconds(%v): expected an error", c.in)
			}
			continue
		}
		if got != c.want {
			t.Errorf("cycleSeconds(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Options must translate into the exact Display the encoder expects.
func TestDisplayFromOptions(t *testing.T) {
	o := Options{Mode: ShowCycle, Cycle: 7 * time.Second, Target: ShowGPU}
	d := o.display()

	if d.Mode != panel.ModeCycle {
		t.Errorf("mode = %d, want %d", d.Mode, panel.ModeCycle)
	}
	if d.CycleSeconds != 7 {
		t.Errorf("cycle = %d, want 7", d.CycleSeconds)
	}
	if d.CPUGPU != panel.GPUOnly {
		t.Errorf("cpu/gpu = %d, want %d", d.CPUGPU, panel.GPUOnly)
	}
}

func TestUnitSymbol(t *testing.T) {
	if got := Celsius.symbol(); got != "C" {
		t.Errorf("celsius symbol = %q, want C", got)
	}
	if got := Fahrenheit.symbol(); got != "F" {
		t.Errorf("fahrenheit symbol = %q, want F", got)
	}
}
