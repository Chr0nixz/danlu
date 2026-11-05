package store

import "slices"

// ResourceProfile 只存汇总数据。不同来源的覆盖时长分开记录。
type ResourceProfile struct {
	GPU  *GPUProfile  `json:"gpu,omitempty"`
	Host *HostProfile `json:"host,omitempty"`
}

type GPUProfile struct {
	AvgUtil        float64       `json:"avg_util"`
	Samples        int64         `json:"samples"`
	CoveredSeconds float64       `json:"covered_seconds"`
	Cards          []CardProfile `json:"cards"`
}

type CardProfile struct {
	Index     int `json:"index"`
	PeakMemMB int `json:"peak_mem_mb"`
	TotalMB   int `json:"total_mem_mb"`
}

type HostProfile struct {
	PeakRSSBytes   int64   `json:"peak_rss_bytes"`
	Samples        int64   `json:"samples"`
	CoveredSeconds float64 `json:"covered_seconds"`
}

func (p *ResourceProfile) Clone() *ResourceProfile {
	if p == nil {
		return nil
	}
	copy := *p
	if p.GPU != nil {
		gpu := *p.GPU
		gpu.Cards = slices.Clone(gpu.Cards)
		copy.GPU = &gpu
	}
	if p.Host != nil {
		host := *p.Host
		copy.Host = &host
	}
	return &copy
}
