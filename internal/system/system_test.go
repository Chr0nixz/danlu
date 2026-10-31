package system

import (
	"context"
	"testing"
)

func TestParseCPUStat(t *testing.T) {
	got, err := parseCPUStat("cpu  100 20 30 400 50 6 7 8 900 100\ncpu0 1 2 3 4 5 6 7 8\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.total != 621 || got.idle != 450 {
		t.Fatalf("parseCPUStat() = %+v, want total=621 idle=450", got)
	}
}

func TestParseMemInfo(t *testing.T) {
	input := "MemTotal:       16384 kB\nMemAvailable:    4096 kB\n"
	total, available, err := parseMemInfo(input)
	if err != nil {
		t.Fatal(err)
	}
	if total != 16*1024*1024 || available != 4*1024*1024 {
		t.Fatalf("parseMemInfo() = %d, %d", total, available)
	}
}

func TestParseMemInfoFallback(t *testing.T) {
	input := "MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 200 kB\nSReclaimable: 30 kB\nShmem: 20 kB\n"
	total, available, err := parseMemInfo(input)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1000*1024 || available != 360*1024 {
		t.Fatalf("fallback parseMemInfo() = %d, %d", total, available)
	}
}

func TestMonitorKeepsOnlyLatestMinute(t *testing.T) {
	mon := NewMonitor(&sequenceSource{})
	for i := 0; i < HistorySize+3; i++ {
		mon.Refresh(context.Background())
	}
	samples, _, err := mon.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != HistorySize || samples[0].MemoryUsedBytes != 3 {
		t.Fatalf("Latest() returned %d samples starting with memory=%d", len(samples), samples[0].MemoryUsedBytes)
	}
}

type sequenceSource struct{ n uint64 }

func (s *sequenceSource) Query(context.Context) (HostSample, error) {
	sample := HostSample{MemoryUsedBytes: s.n}
	s.n++
	return sample, nil
}
