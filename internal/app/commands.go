package app

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/volcie/cooler-lcd-linux/internal/panel"
	"github.com/volcie/cooler-lcd-linux/internal/telemetry"
)

func newPrintCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "print",
		Short: "Show resolved sensors and the encoded frame, without touching the device",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.validate(); err != nil {
				return fmt.Errorf("%w: %w", errConfig, err)
			}
			s := newSampler(opts)
			dump(cmd.OutOrStdout(), s, *opts)
			return nil
		},
	}
}

func newOnceCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "once",
		Short: "Send a single frame and exit, leaving values on screen",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOnce(cmd, *opts)
		},
	}
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List matching cooler display devices",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			devices, err := panel.Enumerate()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(devices) == 0 {
				fmt.Fprintf(out, "no %04x:%04x devices found\n", panel.VendorID, panel.ProductID)
				return nil
			}
			for _, d := range devices {
				fmt.Fprintf(out, "%s  product=%q usage_page=%04x usage=%04x interface=%d\n",
					d.Path, d.Product, d.UsagePage, d.Usage, d.Interface)
			}
			return nil
		},
	}
}

// newSampler primes the delta counters and measures over a short interval, so
// the reported power and usage are real rather than zero.
func newSampler(o *Options) *sampler {
	s := telemetry.New(o.Paths)
	s.Prime()
	time.Sleep(250 * time.Millisecond)
	return s
}

// sampler aliases the telemetry sampler for readability in this package.
type sampler = telemetry.Sampler

// dump reports what the sensors resolved to and prints the exact payload.
func dump(w io.Writer, s *sampler, o Options) {
	paths := s.Paths()
	orDash := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}

	// Sample once. CPU power and usage are deltas: calling them twice would
	// advance their counters and make the printed figures disagree with the
	// payload that follows.
	r := readings(s, o)

	// Show the scale the panel will actually display, so the listing and the
	// "panel would show" line agree.
	unit := "C"
	if o.Unit == Fahrenheit {
		unit = "F"
	}
	cpuShown, gpuShown := r.CPUTemp, r.GPUTemp
	if o.Unit == Fahrenheit {
		cpuShown, gpuShown = panel.ToFahrenheit(cpuShown), panel.ToFahrenheit(gpuShown)
	}

	fmt.Fprintf(w, "cpu temp   %-42s %6.2f %s\n", orDash(paths.CPUTemp), cpuShown, unit)
	fmt.Fprintf(w, "cpu power  %-42s %6.2f W\n", orDash(paths.RAPL), r.CPUPower)
	fmt.Fprintf(w, "cpu freq   %-42s %6.0f MHz\n", freqLabel(o), r.CPUFreqMHz)
	fmt.Fprintf(w, "cpu usage  %-42s %6.1f %%\n", "/proc/stat", r.CPUUsage)
	fmt.Fprintf(w, "gpu hwmon  %-42s\n", orDash(paths.GPU))
	fmt.Fprintf(w, "gpu temp   %-42s %6.2f %s\n", "temp1_input (edge)", gpuShown, unit)
	fmt.Fprintf(w, "gpu power  %-42s %6.2f W\n", "power1_average", r.GPUPower)
	fmt.Fprintf(w, "gpu freq   %-42s %6.0f MHz\n", "freq1_input (sclk)", r.GPUFreqMHz)
	fmt.Fprintf(w, "gpu usage  %-42s %6.1f %%\n", "gpu_busy_percent", r.GPUUsage)
	fmt.Fprintf(w, "fan        %-42s %6.0f RPM\n", orDash(paths.Fan), r.FanRPM)
	fmt.Fprintf(w, "ram usage  %-42s %6.1f %%\n", "/proc/meminfo", r.RAMUsage)

	values := panel.Values(r, o.display())
	payload := panel.Frame(values, panel.OpRefresh)

	fmt.Fprintf(w, "\nvalues  %v\n", values)
	fmt.Fprintf(w, "payload % x\n", payload)

	node := o.Device
	if node == "" {
		if devices, err := panel.Enumerate(); err == nil && len(devices) > 0 {
			node = devices[0].Path
		} else {
			node = "(none found)"
		}
	}
	fmt.Fprintf(w, "device  %s\n", node)

	cpuT, cpuP, gpuP, cpuF, gpuF, fan, pump := panel.Reconstruct(values)
	// Reconstruct returns whatever the panel will display, so the label has to
	// follow the selected unit rather than assuming celsius.
	fmt.Fprintf(w, "\npanel would show: cpu %.2f%s %.0fMHz %.1fW | gpu %.0fMHz %.1fW | fan %.0fRPM pump %.0fRPM\n",
		cpuT, o.Unit.symbol(), cpuF, cpuP, gpuF, gpuP, fan, pump)
}

func freqLabel(o Options) string { return string(o.Freq) }
