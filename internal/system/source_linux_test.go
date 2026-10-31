//go:build linux

package system

import (
	"context"
	"testing"
	"time"
)

func TestProcSourceQuery(t *testing.T) {
	source := &ProcSource{}
	sample, err := source.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sample.MemoryTotalBytes == 0 || sample.MemoryUsedBytes > sample.MemoryTotalBytes || sample.CPUPercent != nil {
		t.Fatalf("first sample is invalid: %+v", sample)
	}

	deadline := time.Now().Add(2 * time.Second)
	for sample.CPUPercent == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		sample, err = source.Query(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	if sample.CPUPercent == nil || *sample.CPUPercent < 0 || *sample.CPUPercent > 100 {
		t.Fatalf("CPU percentage did not become valid: %+v", sample)
	}
}
