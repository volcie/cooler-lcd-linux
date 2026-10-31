package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/volcie/cooler-lcd-linux/internal/panel"
	"github.com/volcie/cooler-lcd-linux/internal/telemetry"
)

// recorder captures every frame written to the fake panel.
type recorder struct {
	mu     sync.Mutex
	frames [][]byte
}

func (r *recorder) Send(payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(payload))
	copy(cp, payload)
	r.frames = append(r.frames, cp)
	return nil
}

func (r *recorder) Path() string { return "/dev/fake" }

func (r *recorder) Close() error { return nil }

func (r *recorder) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.frames))
	copy(out, r.frames)
	return out
}

// newTestSampler points every sensor at a temp-directory fixture so the loop
// tests do not depend on the host's hardware.
func newTestSampler(t *testing.T) *telemetry.Sampler {
	t.Helper()
	dir := t.TempDir()

	cpuTemp := filepath.Join(dir, "cpu_temp")
	gpuDir := filepath.Join(dir, "gpu")
	fan := filepath.Join(dir, "fan")
	rapl := filepath.Join(dir, "rapl")

	files := map[string]string{
		cpuTemp:                                             "56190\n",
		filepath.Join(gpuDir, "temp1_input"):                "33000\n",
		filepath.Join(gpuDir, "power1_average"):             "39000000\n",
		filepath.Join(gpuDir, "freq1_input"):                "1492000000\n",
		filepath.Join(gpuDir, "device", "gpu_busy_percent"): "18\n",
		fan:  "897\n",
		rapl: "1000000\n",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return telemetry.New(telemetry.Paths{CPUTemp: cpuTemp, GPU: gpuDir, Fan: fan, RAPL: rapl})
}

func TestStreamSendsFramesAndBlanksOnCancel(t *testing.T) {
	dev := &recorder{}
	o := Options{Refresh: 20 * time.Millisecond, Mode: ShowTemps, Cycle: 2 * time.Second, Target: ShowBoth}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- stream(ctx, newTestSampler(t), dev, o, log) }()

	time.Sleep(110 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop after cancel")
	}

	frames := dev.snapshot()
	if len(frames) < 2 {
		t.Fatalf("sent %d frames, want at least 2", len(frames))
	}
	for i, f := range frames {
		if len(f) != panel.ReportLen {
			t.Fatalf("frame %d is %d bytes, want %d", i, len(f), panel.ReportLen)
		}
	}

	first := frames[0]
	if first[1] != panel.OpRefresh || first[2] != 0x02 {
		t.Errorf("first frame header = %02x %02x, want 01 02", first[1], first[2])
	}
	if first[3] != 56 || first[4] != 19 {
		t.Errorf("first frame cpu temp = %d.%d, want 56.19", first[3], first[4])
	}

	last := frames[len(frames)-1]
	if last[1] != panel.OpBlank {
		t.Errorf("last frame opcode = %#x, want %#x (blank)", last[1], panel.OpBlank)
	}
	for i := 2; i < panel.ReportLen; i++ {
		if last[i] != 0 {
			t.Fatalf("blank frame byte %d = %#x, want 0", i, last[i])
		}
	}
	// stream deliberately does not close the device; runStream owns that.
}

// A failing send must abort the loop rather than spin.
func TestStreamStopsOnSendError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	o := Options{Refresh: 10 * time.Millisecond}

	err := stream(context.Background(), newTestSampler(t), &failingSender{}, o, log)
	if err == nil {
		t.Fatal("expected an error from a failing panel")
	}
}

type failingSender struct{}

func (f *failingSender) Send([]byte) error { return io.ErrClosedPipe }
func (f *failingSender) Path() string      { return "/dev/failing" }
func (f *failingSender) Close() error      { return nil }

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		ok   bool
	}{
		{"defaults", Options{Refresh: time.Second}, true},
		{"cycle too large", Options{Refresh: time.Second, Cycle: 300 * time.Second}, false},
		{"sub-second cycle", Options{Refresh: time.Second, Cycle: 1500 * time.Millisecond}, false},
		{"zero refresh", Options{Refresh: 0}, false},
		{"cycle", Options{Refresh: time.Second, Mode: ShowCycle, Cycle: 9 * time.Second, Target: ShowCPU}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.validate()
			if c.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
