package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/volcie/cooler-lcd-linux/internal/panel"
)

// The wire protocol encodes display settings as small integers. Those numbers
// are an implementation detail: the CLI exposes words, and this file maps
// between the two.

// TempUnit selects the scale the panel shows.
type TempUnit string

const (
	Celsius    TempUnit = "c"
	Fahrenheit TempUnit = "f"
)

func (u *TempUnit) String() string { return string(*u) }

func (u *TempUnit) Set(s string) error {
	switch strings.ToLower(s) {
	case "c", "celsius":
		*u = Celsius
	case "f", "fahrenheit":
		*u = Fahrenheit
	default:
		return fmt.Errorf("must be c or f, got %q", s)
	}
	return nil
}

func (u TempUnit) Type() string { return "c|f" }

// symbol returns the degree symbol for the selected scale.
func (u TempUnit) symbol() string {
	if u == Fahrenheit {
		return "F"
	}
	return "C"
}

// DisplayMode selects what the panel renders.
type DisplayMode string

const (
	ShowTemps  DisplayMode = "temp"
	ShowUsages DisplayMode = "usage"
	ShowCycle  DisplayMode = "cycle"
)

func (m *DisplayMode) String() string { return string(*m) }

func (m *DisplayMode) Set(s string) error {
	switch strings.ToLower(s) {
	case "temp", "temps", "temperature":
		*m = ShowTemps
	case "usage", "usages":
		*m = ShowUsages
	case "cycle":
		*m = ShowCycle
	default:
		return fmt.Errorf("must be temp, usage or cycle, got %q", s)
	}
	return nil
}

func (m DisplayMode) Type() string { return "temp|usage|cycle" }

// wire maps the mode to its protocol value.
func (m DisplayMode) wire() int {
	switch m {
	case ShowUsages:
		return panel.ModeUsages
	case ShowCycle:
		return panel.ModeCycle
	default:
		return panel.ModeTemps
	}
}

// ShowTarget selects which halves of the display are populated.
type ShowTarget string

const (
	ShowBoth ShowTarget = "both"
	ShowCPU  ShowTarget = "cpu"
	ShowGPU  ShowTarget = "gpu"
)

func (t *ShowTarget) String() string { return string(*t) }

func (t *ShowTarget) Set(s string) error {
	switch strings.ToLower(s) {
	case "both", "all":
		*t = ShowBoth
	case "cpu", "c":
		*t = ShowCPU
	case "gpu", "g":
		*t = ShowGPU
	default:
		return fmt.Errorf("must be both, cpu or gpu, got %q", s)
	}
	return nil
}

func (t ShowTarget) Type() string { return "both|cpu|gpu" }

func (t ShowTarget) wire() int {
	switch t {
	case ShowCPU:
		return panel.CPUOnly
	case ShowGPU:
		return panel.GPUOnly
	default:
		return panel.GPUBoth
	}
}

// FreqMode selects which CPU clock to report.
type FreqMode string

const (
	FreqMax FreqMode = "max"
	FreqAvg FreqMode = "avg"
)

func (f *FreqMode) String() string { return string(*f) }

func (f *FreqMode) Set(s string) error {
	switch strings.ToLower(s) {
	case "max", "highest":
		*f = FreqMax
	case "avg", "average", "mean":
		*f = FreqAvg
	default:
		return fmt.Errorf("must be max or avg, got %q", s)
	}
	return nil
}

func (f FreqMode) Type() string { return "max|avg" }

// cycleSeconds converts the cycle interval to the whole seconds the protocol
// stores, rejecting values the single-byte slot cannot hold.
func cycleSeconds(d time.Duration) (int, error) {
	if d <= 0 {
		return 0, nil // 0 disables cycling
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("must be a whole number of seconds, got %s", d)
	}
	secs := int(d / time.Second)
	if secs > 255 {
		return 0, fmt.Errorf("must be at most 255s, got %ds", secs)
	}
	return secs, nil
}
