// Package telemetry samples host sensors from sysfs and procfs.
//
// Everything here is Linux-specific by nature: CPU package temperature comes
// from k10temp/zenpower/coretemp, CPU power from the RAPL energy counter, GPU
// data from amdgpu's hwmon node, and usage from /proc/stat.
package telemetry

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Paths pins the sensor sources. Empty fields are auto-detected.
type Paths struct {
	CPUTemp string // e.g. /sys/class/hwmon/hwmon4/temp1_input
	GPU     string // amdgpu hwmon directory
	Fan     string // fan tachometer
	Pump    string // pump tachometer
	RAPL    string // energy_uj
}

// Sampler reads sensors. Counters that need a delta (CPU power, CPU usage) keep
// state between calls, so one Sampler should serve one process.
type Sampler struct {
	paths Paths

	raplPrev int64
	raplAt   time.Time

	cpuPrevTotal uint64
	cpuPrevIdle  uint64
	cpuUsagePct  float64
	ramPct       float64

	clock func() time.Time
}

// New resolves sensor paths, falling back to auto-detection for anything the
// caller left empty.
func New(p Paths) *Sampler {
	s := &Sampler{paths: p, clock: time.Now}
	if s.paths.CPUTemp == "" {
		s.paths.CPUTemp = detectCPUTemp()
	}
	if s.paths.GPU == "" {
		s.paths.GPU = detectGPU()
	}
	if s.paths.Fan == "" {
		s.paths.Fan = detectFan()
	}
	if s.paths.RAPL == "" {
		s.paths.RAPL = detectRAPL()
	}
	s.raplPrev, _ = readInt(s.paths.RAPL)
	s.raplAt = s.clock()
	s.cpuPrevTotal, s.cpuPrevIdle, _ = cpuTimes()
	return s
}

// Paths reports the resolved sensor sources, for diagnostics.
func (s *Sampler) Paths() Paths { return s.paths }

// Prime reads the delta counters once so the first Sample reports real values
// instead of zeros.
func (s *Sampler) Prime() {
	s.CPUPower()
	s.CPUUsage()
}

func readInt(path string) (int64, bool) {
	if path == "" {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func readString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func hwmonDir(name string) string {
	dirs, err := filepath.Glob("/sys/class/hwmon/hwmon*")
	if err != nil {
		return ""
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if readString(filepath.Join(d, "name")) == name {
			return d
		}
	}
	return ""
}

// tempByLabel finds temp*_input in dir whose sibling *_label matches.
func tempByLabel(dir, label string) string {
	inputs, err := filepath.Glob(filepath.Join(dir, "temp*_input"))
	if err != nil {
		return ""
	}
	for _, in := range inputs {
		if readString(strings.TrimSuffix(in, "_input")+"_label") == label {
			return in
		}
	}
	return ""
}

// detectCPUTemp prefers the package temperature, which is what the vendor app
// reports, and falls back to the first sensor the chip exposes.
func detectCPUTemp() string {
	for _, name := range []string{"k10temp", "zenpower", "coretemp"} {
		dir := hwmonDir(name)
		if dir == "" {
			continue
		}
		for _, label := range []string{"Tctl", "Package id 0"} {
			if p := tempByLabel(dir, label); p != "" {
				return p
			}
		}
		fallback := filepath.Join(dir, "temp1_input")
		if _, ok := readInt(fallback); ok {
			return fallback
		}
	}
	return ""
}

// detectGPU returns the amdgpu hwmon dir with the most useful sensors. A
// machine with an iGPU has two; the discrete card is the one with power and
// fan telemetry.
func detectGPU() string {
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	sort.Strings(dirs)

	var best string
	bestScore := -1
	for _, d := range dirs {
		if readString(filepath.Join(d, "name")) != "amdgpu" {
			continue
		}
		score := 0
		if _, ok := readInt(filepath.Join(d, "power1_average")); ok {
			score += 2
		}
		if _, ok := readInt(filepath.Join(d, "fan1_input")); ok {
			score++
		}
		if _, ok := readInt(filepath.Join(d, "temp2_input")); ok {
			score++
		}
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

// detectFan returns a spinning tachometer, preferring a SuperIO chip (the
// CPU/board fan) over the GPU fan.
func detectFan() string {
	for _, name := range []string{"nct6775", "nct6798", "it87", "asus_wmi_sensors", "dell_smm", "amdgpu"} {
		dir := hwmonDir(name)
		if dir == "" {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(dir, "fan*_input"))
		for _, in := range inputs {
			if n, ok := readInt(in); ok && n > 0 {
				return in
			}
		}
	}
	inputs, _ := filepath.Glob("/sys/class/hwmon/hwmon*/fan*_input")
	for _, in := range inputs {
		if n, ok := readInt(in); ok && n > 0 {
			return in
		}
	}
	return ""
}

// detectRAPL returns the package-0 energy counter.
func detectRAPL() string {
	dirs, _ := filepath.Glob("/sys/class/powercap/intel-rapl:*")
	sort.Strings(dirs)
	for _, d := range dirs {
		if strings.Count(filepath.Base(d), ":") != 1 {
			continue // subzones such as intel-rapl:0:0
		}
		if readString(filepath.Join(d, "name")) != "package-0" {
			continue
		}
		counter := filepath.Join(d, "energy_uj")
		if _, ok := readInt(counter); ok {
			return counter
		}
	}
	return ""
}

func cpuTimes() (total, idle uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false // covers both EOF and sc.Err()
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, s := range fields[1:] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			continue
		}
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return total, idle, true
}

// ramPercent returns used memory as a percentage of total.
//
// It reports ok=false when the file cannot be read in full: a partial read could
// otherwise leave MemTotal set with MemAvailable still zero, which renders as
// 100% usage rather than as a failure.
func ramPercent() (pct float64, ok bool) {
	return parseMeminfo(func() (io.ReadCloser, error) { return os.Open("/proc/meminfo") })
}

// parseMeminfo reads a /proc/meminfo-format stream. The opener is a parameter so
// tests can supply truncated input.
func parseMeminfo(open func() (io.ReadCloser, error)) (pct float64, ok bool) {
	f, err := open()
	if err != nil {
		return 0, false
	}
	defer f.Close()

	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, haveTotal = n, true
		case "MemAvailable:":
			avail, haveAvail = n, true
		}
	}
	if err := sc.Err(); err != nil {
		return 0, false
	}
	// A missing field is a parse problem, not a reading of zero.
	if !haveTotal || !haveAvail || total == 0 || avail > total {
		return 0, false
	}
	return 100 * float64(total-avail) / float64(total), true
}

// CPUTemp returns the package temperature in celsius.
func (s *Sampler) CPUTemp() float64 {
	if n, ok := readInt(s.paths.CPUTemp); ok {
		return float64(n) / 1000
	}
	return 0
}

// CPUPower derives watts from the RAPL energy counter delta. A wrapped or reset
// counter yields 0 rather than a nonsense spike.
func (s *Sampler) CPUPower() float64 {
	now, ok := readInt(s.paths.RAPL)
	if !ok {
		return 0
	}
	at := s.clock()
	dt := at.Sub(s.raplAt).Seconds()
	if dt <= 0 {
		return 0
	}
	de := now - s.raplPrev
	s.raplPrev, s.raplAt = now, at
	if de < 0 {
		return 0
	}
	return float64(de) / 1e6 / dt
}

// CPUUsage returns the busy fraction since the previous call.
func (s *Sampler) CPUUsage() float64 {
	total, idle, ok := cpuTimes()
	if !ok {
		return s.cpuUsagePct
	}
	dt := total - s.cpuPrevTotal
	di := idle - s.cpuPrevIdle
	s.cpuPrevTotal, s.cpuPrevIdle = total, idle
	if dt == 0 || di > dt {
		return s.cpuUsagePct
	}
	s.cpuUsagePct = 100 * float64(dt-di) / float64(dt)
	return s.cpuUsagePct
}

// CPUFreqMHz reports the highest core clock, or the mean when avg is true.
func (s *Sampler) CPUFreqMHz(avg bool) float64 {
	paths, _ := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq")
	var sum, max, n float64
	for _, p := range paths {
		v, ok := readInt(p)
		if !ok || v <= 0 {
			continue
		}
		mhz := float64(v) / 1000
		sum += mhz
		if mhz > max {
			max = mhz
		}
		n++
	}
	if n == 0 {
		return 0
	}
	if avg {
		return sum / n
	}
	return max
}

// CPUVolt returns core voltage in volts. Most consumer boards expose this only
// through a SuperIO driver, so it is usually unavailable.
func (s *Sampler) CPUVolt() float64 { return 0 }

// GPUTemp returns the GPU edge temperature in celsius.
func (s *Sampler) GPUTemp() float64 {
	if n, ok := readInt(filepath.Join(s.paths.GPU, "temp1_input")); ok {
		return float64(n) / 1000
	}
	return 0
}

// GPUUsage returns GPU busy percentage.
func (s *Sampler) GPUUsage() float64 {
	if n, ok := readInt(filepath.Join(s.paths.GPU, "device", "gpu_busy_percent")); ok {
		return float64(n)
	}
	return 0
}

// GPUPower returns board power in watts.
func (s *Sampler) GPUPower() float64 {
	if n, ok := readInt(filepath.Join(s.paths.GPU, "power1_average")); ok {
		return float64(n) / 1e6
	}
	return 0
}

// GPUFreqMHz returns the current graphics clock in MHz.
func (s *Sampler) GPUFreqMHz() float64 {
	if n, ok := readInt(filepath.Join(s.paths.GPU, "freq1_input")); ok {
		return float64(n) / 1e6
	}
	return 0
}

// FanRPM returns the fan tachometer reading.
func (s *Sampler) FanRPM() float64 {
	if n, ok := readInt(s.paths.Fan); ok {
		return float64(n)
	}
	return 0
}

// PumpRPM returns the pump tachometer reading, if one is configured.
func (s *Sampler) PumpRPM() float64 {
	if n, ok := readInt(s.paths.Pump); ok {
		return float64(n)
	}
	return 0
}

// RAMPercent returns used memory percentage, falling back to the previous
// reading when /proc/meminfo cannot be read in full.
func (s *Sampler) RAMPercent() float64 {
	if pct, ok := ramPercent(); ok {
		s.ramPct = pct
	}
	return s.ramPct
}

// Now returns the sampler's clock reading.
func (s *Sampler) Now() time.Time { return s.clock() }
