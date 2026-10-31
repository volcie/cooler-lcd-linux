package panel

// Display describes what the panel should render, as opposed to the sensor
// values it renders. These map to the three non-telemetry slots.
type Display struct {
	Mode         int // ModeTemps, ModeUsages or ModeCycle
	CycleSeconds int
	CPUGPU       int // GPUBoth, CPUOnly or GPUOnly
}

// Frame builds the 64-byte report payload. values may be shorter than
// ValueCount; the remainder stays zero, matching the vendor's zeroed array.
func Frame(values []byte, opcode byte) []byte {
	frame := make([]byte, ReportLen)
	FrameInto(frame, values, opcode)
	return frame
}

// FrameInto builds the payload in dst, which must be ReportLen bytes. Reusing a
// caller-owned buffer keeps the 5 Hz send loop allocation-free.
func FrameInto(dst, values []byte, opcode byte) {
	clear(dst[:ReportLen])
	dst[1] = opcode
	if opcode == OpRefresh {
		dst[2] = 0x02
		copy(dst[3:3+ValueCount], values)
	}
}

// ClampByte mirrors the vendor's Convert::ToInt16 followed by a store into a
// byte array: negatives and overflow saturate rather than wrap.
func ClampByte(v float64) byte {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return byte(v)
	}
}

// ToFahrenheit converts celsius to fahrenheit. The vendor app applies this to
// the sensor value before encoding it (GetF: c * 1.8 + 32) and sets the unit
// slot; doing one without the other makes the panel show a celsius number
// labelled as fahrenheit.
func ToFahrenheit(c float64) float64 { return c*1.8 + 32 }

// Split returns the integer part and hundredths of v.
//
// The firmware renders these as separate digits, so the split must round rather
// than truncate: 56.19 stored as float64 is 0.18999..., which would truncate to
// 18 and display 56.18.
func Split(v float64) (whole, hundredths int) {
	if v < 0 {
		v = 0
	}
	h := int(v*100 + 0.5)
	return h / 100, h % 100
}

// Reconstruct undoes the split encoding the way the panel firmware does, which
// is how tests and -print prove the encoding round-trips.
func Reconstruct(v []byte) (cpuTemp, cpuPower, gpuPower, cpuFreq, gpuFreq, fanRPM, pumpRPM float64) {
	cpuTemp = float64(v[SlotCPUTemp]) + float64(v[SlotCPUTempFrac])/100
	cpuPower = float64(v[SlotCPUPowerHigh])*100 + float64(v[SlotCPUPower]) + float64(v[SlotCPUPowerFrac])/100
	gpuPower = float64(v[SlotGPUPowerHigh])*100 + float64(v[SlotGPUPower]) + float64(v[SlotGPUPowerFrac])/100
	cpuFreq = float64(v[SlotCPUFreq])*100 + float64(v[SlotCPUFreqRem])
	gpuFreq = float64(v[SlotGPUFreq])*100 + float64(v[SlotGPUFreqRem])
	fanRPM = float64(v[SlotFanRPM])*100 + float64(v[SlotFanRPMRem])
	pumpRPM = float64(v[SlotPumpRPM])*100 + float64(v[SlotPumpRPMRem])
	return
}
