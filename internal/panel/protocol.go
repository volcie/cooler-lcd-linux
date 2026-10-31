// Package panel speaks to cooler LCD panels built on the HID 5131:2007
// controller.
//
// The panel firmware renders its own segmented digits; the host only streams a
// fixed-size report of decimal-split sensor values. See docs/PROTOCOL.md.
package panel

// Device identity. The vendor's own application matches on these two values
// alone, with no model check, so every cooler sharing the controller uses the
// same wire format.
const (
	VendorID  uint16 = 0x5131
	ProductID uint16 = 0x2007
)

// ReportLen is the HID output report payload, excluding the report ID.
const ReportLen = 64

// ValueCount is the vendor's SendValueArray length. Only slots 0..35 carry
// data; the remainder stays zero.
const ValueCount = 60

// Frame opcodes. The vendor app only ever sends Refresh from SendData2 and
// Blank from frmMain_FormClosing.
const (
	OpRefresh byte = 0x01
	OpBlank   byte = 0x0F
)

// Value slot indices. The panel firmware reconstructs each quantity from its
// split bytes, so these positions are a wire format, not an internal detail.
const (
	SlotCPUTemp      = 0 // integer part
	SlotCPUTempFrac  = 1 // hundredths
	SlotCPUUnit      = 2 // 0 celsius, 1 fahrenheit
	SlotCPUUsage     = 3 // percent
	SlotCPUPower     = 4 // watts % 100
	SlotCPUPowerFrac = 5 // hundredths
	SlotCPUFreq      = 6 // MHz / 100
	SlotCPUFreqRem   = 7 // MHz % 100
	SlotCPUVolt      = 8 // volts, integer part
	SlotCPUVoltQuirk = 9 // (volts integer part) * 100 -- vendor quirk
	SlotGPUTemp      = 10
	SlotGPUTempFrac  = 11
	SlotGPUUnit      = 12
	SlotGPUUsage     = 13
	SlotGPUPower     = 14
	SlotGPUPowerFrac = 15
	SlotGPUFreq      = 16
	SlotGPUFreqRem   = 17
	SlotFanRPM       = 18 // RPM / 100
	SlotFanRPMRem    = 19 // RPM % 100
	SlotPumpRPM      = 20
	SlotPumpRPMRem   = 21
	SlotYearHigh     = 22
	SlotYearLow      = 23
	SlotMonth        = 24
	SlotDay          = 25
	SlotHour         = 26
	SlotMinute       = 27
	SlotSecond       = 28
	SlotWeekday      = 29 // .NET DayOfWeek: Sunday == 0
	SlotRAMUsage     = 30
	SlotCPUPowerHigh = 31 // watts / 100
	SlotGPUPowerHigh = 32
	SlotShowMode     = 33 // 0 temps, 1 usages, 2 cycle
	SlotCycleSeconds = 34
	SlotCPUGPU       = 35 // 0 both, 1 cpu, 2 gpu
)

// Display modes for SlotShowMode (vendor: ShowMode).
const (
	ModeTemps  = 0
	ModeUsages = 1
	ModeCycle  = 2
)

// CPU/GPU selection for SlotCPUGPU (vendor: CPU_GPU_Flag).
const (
	GPUBoth = 0
	CPUOnly = 1
	GPUOnly = 2
)
