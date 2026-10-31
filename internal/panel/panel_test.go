package panel

import (
	"sync"
	"testing"
	"time"
)

func testReadings() Readings {
	return Readings{
		CPUTemp:    56.19,
		CPUUsage:   42.6,
		CPUPower:   123.45,
		CPUFreqMHz: 2534,
		GPUUsage:   77.7,
		GPUTemp:    61.99,
		GPUPower:   246.75,
		GPUFreqMHz: 1492,
		FanRPM:     1234,
		PumpRPM:    987,
		RAMUsage:   48.5,
		Now:        time.Date(2026, 10, 31, 12, 45, 30, 0, time.UTC),
	}
}

func testDisplay() Display {
	return Display{Mode: ModeTemps, CycleSeconds: 2, CPUGPU: GPUBoth}
}

func TestFrameShape(t *testing.T) {
	values := make([]byte, ValueCount)
	for i := range values {
		values[i] = byte(i + 1)
	}
	frame := Frame(values, OpRefresh)

	if len(frame) != ReportLen {
		t.Fatalf("frame is %d bytes, want %d", len(frame), ReportLen)
	}
	if frame[0] != 0x00 {
		t.Errorf("frame[0] = %#x, want 0x00", frame[0])
	}
	if frame[1] != OpRefresh {
		t.Errorf("frame[1] = %#x, want %#x", frame[1], OpRefresh)
	}
	if frame[2] != 0x02 {
		t.Errorf("frame[2] = %#x, want 0x02", frame[2])
	}
	for i := range ValueCount {
		if frame[3+i] != values[i] {
			t.Fatalf("frame[%d] = %#x, want %#x", 3+i, frame[3+i], values[i])
		}
	}
	if frame[63] != 0x00 {
		t.Errorf("frame[63] = %#x, want 0x00", frame[63])
	}
}

// The vendor sets array[1] = 15 and leaves every other byte zero
// (frmMain_FormClosing).
func TestBlankFrame(t *testing.T) {
	frame := Frame(nil, OpBlank)
	want := make([]byte, ReportLen)
	want[1] = 0x0F
	for i := range frame {
		if frame[i] != want[i] {
			t.Fatalf("frame[%d] = %#x, want %#x", i, frame[i], want[i])
		}
	}
}

func TestFramePadsShortValues(t *testing.T) {
	frame := Frame([]byte{1, 2, 3}, OpRefresh)
	if len(frame) != ReportLen {
		t.Fatalf("frame is %d bytes, want %d", len(frame), ReportLen)
	}
	if frame[3] != 1 || frame[4] != 2 || frame[5] != 3 {
		t.Errorf("leading values misplaced: % x", frame[3:6])
	}
	for i := 6; i < ReportLen; i++ {
		if frame[i] != 0 {
			t.Fatalf("frame[%d] = %#x, want 0 (padding)", i, frame[i])
		}
	}
}

func TestSplitRoundsRatherThanTruncates(t *testing.T) {
	cases := []struct {
		in   float64
		w, h int
	}{
		{56.19, 56, 19},
		{61.99, 61, 99},
		{0.1, 0, 10},
		{99.995, 100, 0},
		{0, 0, 0},
		{-3, 0, 0},
	}
	for _, c := range cases {
		w, h := Split(c.in)
		if w != c.w || h != c.h {
			t.Errorf("Split(%v) = %d,%d; want %d,%d", c.in, w, h, c.w, c.h)
		}
	}
}

func TestSplitRoundTrip(t *testing.T) {
	for _, in := range []float64{0, 1.05, 33.33, 56.19, 99.99, 255} {
		w, h := Split(in)
		got := float64(w) + float64(h)/100
		if diff := got - in; diff > 0.005 || diff < -0.005 {
			t.Errorf("round trip %v -> %d.%02d = %v", in, w, h, got)
		}
	}
}

func TestClampByte(t *testing.T) {
	cases := []struct {
		in   float64
		want byte
	}{
		{-5, 0}, {0, 0}, {12, 12}, {255, 255}, {300, 255}, {1e9, 255},
	}
	for _, c := range cases {
		if got := ClampByte(c.in); got != c.want {
			t.Errorf("ClampByte(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestValuesEncodesReadings(t *testing.T) {
	v := Values(testReadings(), testDisplay())

	if v[SlotCPUTemp] != 56 || v[SlotCPUTempFrac] != 19 {
		t.Errorf("cpu temp = %d.%d, want 56.19", v[SlotCPUTemp], v[SlotCPUTempFrac])
	}
	if v[SlotGPUTemp] != 61 || v[SlotGPUTempFrac] != 99 {
		t.Errorf("gpu temp = %d.%d, want 61.99", v[SlotGPUTemp], v[SlotGPUTempFrac])
	}
	if v[SlotCPUUsage] != 42 || v[SlotGPUUsage] != 77 {
		t.Errorf("usage = %d,%d; want 42,77", v[SlotCPUUsage], v[SlotGPUUsage])
	}
	if v[SlotFanRPM] != 12 || v[SlotFanRPMRem] != 34 {
		t.Errorf("fan = %d,%d; want 12,34", v[SlotFanRPM], v[SlotFanRPMRem])
	}
	if v[SlotPumpRPM] != 9 || v[SlotPumpRPMRem] != 87 {
		t.Errorf("pump = %d,%d; want 9,87", v[SlotPumpRPM], v[SlotPumpRPMRem])
	}
	if v[SlotYearHigh] != 20 || v[SlotYearLow] != 26 {
		t.Errorf("year = %d%d, want 2026", v[SlotYearHigh], v[SlotYearLow])
	}
	if v[SlotMonth] != 10 || v[SlotDay] != 31 {
		t.Errorf("date = %d/%d, want 10/31", v[SlotMonth], v[SlotDay])
	}
	if v[SlotHour] != 12 || v[SlotMinute] != 45 || v[SlotSecond] != 30 {
		t.Errorf("time = %d:%d:%d, want 12:45:30", v[SlotHour], v[SlotMinute], v[SlotSecond])
	}
	if v[SlotWeekday] != 6 {
		t.Errorf("weekday = %d, want 6 (Saturday, Sunday == 0)", v[SlotWeekday])
	}
}

func TestValuesEncodesDisplaySettings(t *testing.T) {
	d := Display{Mode: ModeCycle, CycleSeconds: 5, CPUGPU: GPUOnly}
	v := Values(testReadings(), d)

	if v[SlotShowMode] != ModeCycle {
		t.Errorf("show mode = %d, want %d", v[SlotShowMode], ModeCycle)
	}
	if v[SlotCycleSeconds] != 5 {
		t.Errorf("cycle seconds = %d, want 5", v[SlotCycleSeconds])
	}
	if v[SlotCPUGPU] != GPUOnly {
		t.Errorf("cpu/gpu = %d, want %d", v[SlotCPUGPU], GPUOnly)
	}
}

func TestValuesFahrenheit(t *testing.T) {
	r := testReadings()
	r.Fahrenheit = true
	v := Values(r, testDisplay())

	if v[SlotCPUUnit] != 1 || v[SlotGPUUnit] != 1 {
		t.Errorf("units = %d,%d; want 1,1", v[SlotCPUUnit], v[SlotGPUUnit])
	}
	// The unit slot only relabels; the value must be converted as well.
	// 56.19C = 133.14F and 61.99C = 143.58F.
	if v[SlotCPUTemp] != 133 || v[SlotCPUTempFrac] != 14 {
		t.Errorf("cpu temp = %d.%02d, want 133.14", v[SlotCPUTemp], v[SlotCPUTempFrac])
	}
	if v[SlotGPUTemp] != 143 || v[SlotGPUTempFrac] != 58 {
		t.Errorf("gpu temp = %d.%02d, want 143.58", v[SlotGPUTemp], v[SlotGPUTempFrac])
	}
}

// A 32C GPU must read 89F, not 32.
func TestFahrenheitConvertsNotJustLabels(t *testing.T) {
	r := Readings{GPUTemp: 32, Now: time.Unix(0, 0)}
	v := Values(r, testDisplay())
	if v[SlotGPUTemp] != 32 || v[SlotGPUUnit] != 0 {
		t.Fatalf("celsius baseline: temp=%d unit=%d, want 32,0", v[SlotGPUTemp], v[SlotGPUUnit])
	}

	r.Fahrenheit = true
	v = Values(r, testDisplay())
	if v[SlotGPUUnit] != 1 {
		t.Errorf("unit = %d, want 1", v[SlotGPUUnit])
	}
	if v[SlotGPUTemp] != 89 || v[SlotGPUTempFrac] != 60 {
		t.Errorf("gpu temp = %d.%02d, want 89.60", v[SlotGPUTemp], v[SlotGPUTempFrac])
	}
}

func TestToFahrenheit(t *testing.T) {
	cases := []struct{ c, f float64 }{
		{0, 32},
		{100, 212},
		{32, 89.6},
		{-40, -40},
	}
	for _, c := range cases {
		if got := ToFahrenheit(c.c); got != c.f {
			t.Errorf("ToFahrenheit(%v) = %v, want %v", c.c, got, c.f)
		}
	}
}

func TestValuesLeavesUnusedSlotsZero(t *testing.T) {
	v := Values(testReadings(), testDisplay())
	if len(v) != ValueCount {
		t.Fatalf("values is %d bytes, want %d", len(v), ValueCount)
	}
	for i := SlotCPUGPU + 1; i < ValueCount; i++ {
		if v[i] != 0 {
			t.Fatalf("slot %d = %#x, want 0 (reserved)", i, v[i])
		}
	}
}

func TestPowerReconstructsAcrossHundreds(t *testing.T) {
	v := Values(testReadings(), testDisplay())
	// 123.45 W -> hundreds 1, tens/units 23, hundredths 45.
	if v[SlotCPUPowerHigh] != 1 || v[SlotCPUPower] != 23 || v[SlotCPUPowerFrac] != 45 {
		t.Errorf("cpu power slots = %d,%d,%d; want 1,23,45",
			v[SlotCPUPowerHigh], v[SlotCPUPower], v[SlotCPUPowerFrac])
	}
	_, cpuPower, gpuPower, _, _, _, _ := Reconstruct(v)
	if cpuPower != 123.45 {
		t.Errorf("cpu power reconstructs to %v, want 123.45", cpuPower)
	}
	if gpuPower != 246.75 {
		t.Errorf("gpu power reconstructs to %v, want 246.75", gpuPower)
	}
}

// fakeTransport records exactly what reaches hid_write.
type fakeTransport struct {
	mu   sync.Mutex
	bufs [][]byte
}

// Write copies the buffer: the Device reuses one wire buffer, so retaining the
// slice would alias every recorded frame to the latest write.
func (f *fakeTransport) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	f.bufs = append(f.bufs, cp)
	return len(p), nil
}

func (f *fakeTransport) Close() error { return nil }

// hidapi requires the report ID as the first byte and one extra byte of length.
// Getting this wrong does not error inside hidapi: it reinterprets payload[0] as
// the report ID and transmits 63 bytes, shifting every field by one slot.
func TestSendPrefixesReportID(t *testing.T) {
	ft := &fakeTransport{}
	d := &Device{dev: ft, path: "/dev/fake"}

	values := Values(testReadings(), testDisplay())
	payload := Frame(values, OpRefresh)
	if err := d.Send(payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(ft.bufs) != 1 {
		t.Fatalf("wrote %d buffers, want 1", len(ft.bufs))
	}
	buf := ft.bufs[0]
	if len(buf) != WireLen {
		t.Fatalf("wrote %d bytes, want %d (report ID + %d-byte report)", len(buf), WireLen, ReportLen)
	}
	if buf[0] != 0x00 {
		t.Errorf("report ID = %#x, want 0x00", buf[0])
	}
	if buf[1] != 0x00 {
		t.Errorf("payload[0] = %#x, want 0x00 (not the opcode)", buf[1])
	}
	if buf[2] != OpRefresh {
		t.Errorf("payload[1] = %#x, want %#x (opcode)", buf[2], OpRefresh)
	}
	if buf[3] != 0x02 {
		t.Errorf("payload[2] = %#x, want 0x02", buf[3])
	}
	// Sensor values must start at payload[3] == wire[4].
	if buf[4] != byte(int(testReadings().CPUTemp)) {
		t.Errorf("cpu temp integer at wire[4] = %d, want %d", buf[4], int(testReadings().CPUTemp))
	}
}

// The blank frame must put 0x0F in payload[1]. If the report ID is missing, the
// opcode lands in payload[0] and the panel treats it as a segment self-test.
func TestBlankFrameKeepsOpcodeInSlotOne(t *testing.T) {
	ft := &fakeTransport{}
	d := &Device{dev: ft, path: "/dev/fake"}

	if err := d.Send(Frame(nil, OpBlank)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	buf := ft.bufs[0]
	if len(buf) != WireLen {
		t.Fatalf("wrote %d bytes, want %d", len(buf), WireLen)
	}
	if buf[0] != 0x00 {
		t.Errorf("wire[0] (report ID) = %#x, want 0x00", buf[0])
	}
	if buf[1] != 0x00 {
		t.Errorf("wire[1] (payload[0]) = %#x, want 0x00", buf[1])
	}
	if buf[2] != OpBlank {
		t.Errorf("wire[2] (payload[1]) = %#x, want %#x", buf[2], OpBlank)
	}
	for i := 3; i < len(buf); i++ {
		if buf[i] != 0 {
			t.Fatalf("wire[%d] = %#x, want 0", i, buf[i])
		}
	}
}

func TestSendRejectsWrongSize(t *testing.T) {
	ft := &fakeTransport{}
	d := &Device{dev: ft, path: "/dev/fake"}
	if err := d.Send(make([]byte, 32)); err == nil {
		t.Fatal("expected an error for a short payload")
	}
}

// A zero time.Time has Weekday == -1; it must not wrap to 255.
func TestWeekdayNeverUnderflows(t *testing.T) {
	// Readings{} leaves Now as the zero time, which is a plausible caller bug.
	v := Values(Readings{}, testDisplay())
	if v[SlotWeekday] > 6 {
		t.Errorf("weekday = %d, want 0..6", v[SlotWeekday])
	}

	// Every real weekday maps to its .NET equivalent: Sunday == 0.
	for day := time.Sunday; day <= time.Saturday; day++ {
		now := time.Date(2026, 11, 1+int(day), 12, 0, 0, 0, time.UTC) // Nov 1 2026 is a Sunday
		v := Values(Readings{Now: now}, testDisplay())
		if int(v[SlotWeekday]) != int(day) {
			t.Errorf("%v encoded as %d, want %d", day, v[SlotWeekday], day)
		}
	}
}

// The wire buffer must be reused, and every frame must start with the report ID
// even after a previous frame left data in it.
func TestSendReusesBufferWithoutLeaking(t *testing.T) {
	ft := &fakeTransport{}
	d := &Device{dev: ft, path: "/dev/fake"}

	full := make([]byte, ReportLen)
	for i := range full {
		full[i] = 0xAB
	}
	full[1] = OpRefresh
	if err := d.Send(full); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := d.Send(Frame(nil, OpBlank)); err != nil {
		t.Fatalf("Send blank: %v", err)
	}

	blank := ft.bufs[1]
	if len(blank) != WireLen {
		t.Fatalf("blank frame is %d bytes, want %d", len(blank), WireLen)
	}
	if blank[0] != 0x00 {
		t.Errorf("report ID = %#x, want 0", blank[0])
	}
	if blank[1] != 0x00 {
		t.Errorf("payload[0] = %#x, want 0", blank[1])
	}
	if blank[2] != OpBlank {
		t.Errorf("payload[1] = %#x, want %#x", blank[2], OpBlank)
	}
	// Everything after the opcode must be zero: a reused buffer would leave
	// the previous frame's sensor bytes here.
	for i := 3; i < WireLen; i++ {
		if blank[i] != 0 {
			t.Fatalf("wire[%d] = %#x leaked from the previous frame", i, blank[i])
		}
	}
}
