package panel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sstallion/go-hid"
)

// Readings are the sensor values to publish, in the units the panel expects.
type Readings struct {
	CPUTemp    float64 // degrees celsius
	CPUUsage   float64 // percent
	CPUPower   float64 // watts
	CPUFreqMHz float64
	CPUVolt    float64 // volts, 0 if unavailable
	GPUTemp    float64
	GPUUsage   float64
	GPUPower   float64
	GPUFreqMHz float64
	FanRPM     float64
	PumpRPM    float64
	RAMUsage   float64 // percent
	Fahrenheit bool
	Now        time.Time
}

// Values encodes readings into the 60 value slots, applying display settings.
func Values(r Readings, d Display) []byte {
	v := make([]byte, ValueCount)
	ValuesInto(v, r, d)
	return v
}

// ValuesInto encodes readings into dst, which must be ValueCount bytes. The send
// loop reuses one buffer instead of allocating a slice per frame.
func ValuesInto(v []byte, r Readings, d Display) {

	unit := byte(0)
	if r.Fahrenheit {
		unit = 1
	}

	cpuT := r.CPUTemp
	gpuT := r.GPUTemp
	if r.Fahrenheit {
		// The unit slot only relabels the display; the value itself must be
		// converted too, as the vendor app does.
		cpuT = ToFahrenheit(cpuT)
		gpuT = ToFahrenheit(gpuT)
	}

	cpuTi, cpuTh := Split(cpuT)
	cpuPi, cpuPh := Split(r.CPUPower)
	gpuTi, gpuTh := Split(gpuT)
	gpuPi, gpuPh := Split(r.GPUPower)

	cpuMHz := int(r.CPUFreqMHz)
	gpuMHz := int(r.GPUFreqMHz)

	v[SlotCPUTemp] = ClampByte(float64(cpuTi))
	v[SlotCPUTempFrac] = ClampByte(float64(cpuTh))
	v[SlotCPUUnit] = unit
	v[SlotCPUUsage] = ClampByte(r.CPUUsage)
	v[SlotCPUPower] = ClampByte(float64(cpuPi % 100))
	v[SlotCPUPowerFrac] = ClampByte(float64(cpuPh))
	v[SlotCPUPowerHigh] = ClampByte(float64(cpuPi / 100))
	v[SlotCPUFreq] = ClampByte(float64(cpuMHz / 100))
	v[SlotCPUFreqRem] = ClampByte(float64(cpuMHz % 100))
	v[SlotCPUVolt] = ClampByte(r.CPUVolt)
	v[SlotCPUVoltQuirk] = ClampByte(r.CPUVolt * 100)

	v[SlotGPUTemp] = ClampByte(float64(gpuTi))
	v[SlotGPUTempFrac] = ClampByte(float64(gpuTh))
	v[SlotGPUUnit] = unit
	v[SlotGPUUsage] = ClampByte(r.GPUUsage)
	v[SlotGPUPower] = ClampByte(float64(gpuPi % 100))
	v[SlotGPUPowerFrac] = ClampByte(float64(gpuPh))
	v[SlotGPUPowerHigh] = ClampByte(float64(gpuPi / 100))
	v[SlotGPUFreq] = ClampByte(float64(gpuMHz / 100))
	v[SlotGPUFreqRem] = ClampByte(float64(gpuMHz % 100))

	fan := int(r.FanRPM)
	v[SlotFanRPM] = ClampByte(float64(fan / 100))
	v[SlotFanRPMRem] = ClampByte(float64(fan % 100))
	pump := int(r.PumpRPM)
	v[SlotPumpRPM] = ClampByte(float64(pump / 100))
	v[SlotPumpRPMRem] = ClampByte(float64(pump % 100))

	v[SlotYearHigh] = ClampByte(float64(r.Now.Year() / 100))
	v[SlotYearLow] = ClampByte(float64(r.Now.Year() % 100))
	v[SlotMonth] = ClampByte(float64(r.Now.Month()))
	v[SlotDay] = ClampByte(float64(r.Now.Day()))
	v[SlotHour] = ClampByte(float64(r.Now.Hour()))
	v[SlotMinute] = ClampByte(float64(r.Now.Minute()))
	v[SlotSecond] = ClampByte(float64(r.Now.Second()))
	// Go's Weekday is Sunday==0, same as .NET's DayOfWeek. Mask into the byte
	// explicitly: a zero time.Time carries Weekday -1, which ClampByte would
	// turn into 255.
	v[SlotWeekday] = ClampByte(float64(int(r.Now.Weekday()) & 0x7))
	v[SlotRAMUsage] = ClampByte(r.RAMUsage)

	v[SlotShowMode] = ClampByte(float64(d.Mode))
	v[SlotCycleSeconds] = ClampByte(float64(d.CycleSeconds))
	v[SlotCPUGPU] = ClampByte(float64(d.CPUGPU))
}

// transport is the write side of a HID handle. Tests substitute a recorder.
type transport interface {
	Write([]byte) (int, error)
	Close() error
}

// WireLen is what hid_write expects: the report ID followed by the report.
const WireLen = ReportLen + 1

// Device is an open handle to the panel.
type Device struct {
	dev  transport
	path string
	buf  []byte // report ID + payload, reused across frames
}

// ErrPermission is returned when this user may not open the hidraw node.
var ErrPermission = errors.New("permission denied")

// isPermissionDenied reports whether err is hidapi's permission failure.
// hidapi surfaces errors as plain strings, so this is a prefix test on the
// message it returns for a non-writable hidraw node.
func isPermissionDenied(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

// Open opens the first device matching the controller.
func Open() (*Device, error) {
	devices, err := Enumerate()
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no %04x:%04x HID device found", VendorID, ProductID)
	}
	return OpenPath(devices[0].Path)
}

// OpenPath opens a specific hidraw node.
func OpenPath(node string) (*Device, error) {
	dev, err := hid.OpenPath(node)
	if err != nil {
		if isPermissionDenied(err) {
			return nil, fmt.Errorf("%w: %s", ErrPermission, node)
		}
		return nil, fmt.Errorf("open %s: %w", node, err)
	}
	return &Device{dev: dev, path: node, buf: make([]byte, WireLen)}, nil
}

// Path reports the node path the library resolved, if any.
func (d *Device) Path() string { return d.path }

// Send writes one frame.
//
// hidapi requires the report ID as the first byte of the buffer and always
// transmits one byte more than the report itself (see hidapi.h: "The first byte
// of data[] must contain the Report ID... calls to hid_write() will always
// contain one more byte than the report contains"). The ID is zero because the
// descriptor declares no numbered reports.
//
// Omitting that byte does not fail: hidapi quietly reinterprets the payload's
// first byte as the report ID and transmits the remaining 63, shifting every
// field one slot. The panel then renders nothing.
func (d *Device) Send(payload []byte) error {
	if len(payload) != ReportLen {
		return fmt.Errorf("payload must be %d bytes, got %d", ReportLen, len(payload))
	}
	if d.buf == nil {
		d.buf = make([]byte, WireLen)
	}
	// Clear first: a reused buffer would otherwise carry the previous frame's
	// bytes into the blank frame.
	clear(d.buf)
	copy(d.buf[1:], payload)

	n, err := d.dev.Write(d.buf)
	if err != nil {
		return fmt.Errorf("write %s: %w", d.path, err)
	}
	if n != WireLen {
		return fmt.Errorf("short write to %s: %d/%d bytes", d.path, n, WireLen)
	}
	return nil
}

// Close releases the device.
func (d *Device) Close() error { return d.dev.Close() }

// Info describes every matching device, for -list and diagnostics.
type Info struct {
	Path      string
	Product   string
	UsagePage uint16
	Usage     uint16
	Interface int
}

// Enumerate lists matching devices.
func Enumerate() ([]Info, error) {
	var out []Info
	err := hid.Enumerate(VendorID, ProductID, func(d *hid.DeviceInfo) error {
		out = append(out, Info{
			Path:      d.Path,
			Product:   d.ProductStr,
			UsagePage: d.UsagePage,
			Usage:     d.Usage,
			Interface: d.InterfaceNbr,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate: %w", err)
	}
	return out, nil
}
