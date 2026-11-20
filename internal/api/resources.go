package api

import (
	"net/http"
	"time"

	"danlu/internal/system"
)

type resourceResponse struct {
	*system.ResourceSnapshot
	UpdatedAt      int64         `json:"updated_at"`
	Disks          []system.Disk `json:"disks"`
	DisksUpdatedAt int64         `json:"disks_updated_at"`
	DisksError     string        `json:"disks_error,omitempty"`
	Error          string        `json:"error,omitempty"`
}

func (s *Server) resources(w http.ResponseWriter, r *http.Request) {
	if s.Resources == nil {
		writeErr(w, http.StatusServiceUnavailable, "资源采集尚未启动")
		return
	}
	// updated_at 用快照自己的采样时间，不用 Sampler 的完成时间：采集失败时 Sampler 也会刷新时间，
	// 返回的却是上一份好快照（第一次就失败时是零值），面板会把旧数据甚至一屏 0 标成"1 秒前"。
	snap, _, err := s.Resources.Latest()
	if snap.CPU.Cores == nil {
		snap.CPU.Cores = []int{}
	}
	if snap.Network == nil {
		snap.Network = []system.NetIface{}
	}
	if snap.DiskIO == nil {
		snap.DiskIO = []system.DiskIO{}
	}
	if snap.Processes == nil {
		snap.Processes = []system.ProcessInfo{}
	}
	if s.Store != nil {
		// 一个任务常有多个进程同时进榜，按任务号记一次，别对同一个任务重复查库。
		names := map[int64]string{}
		for i := range snap.Processes {
			id := snap.Processes[i].JobID
			if id <= 0 {
				continue
			}
			name, seen := names[id]
			if !seen {
				if job, getErr := s.Store.GetJob(id); getErr == nil {
					name = job.Name
				}
				names[id] = name
			}
			snap.Processes[i].JobName = name
		}
	}
	disks := []system.Disk{}
	disksAt := time.Time{}
	disksErr := ""
	if s.Disks != nil {
		var diskAt time.Time
		var diskErr error
		disks, diskAt, diskErr = s.Disks.Latest()
		if disks == nil {
			disks = []system.Disk{}
		}
		disksAt = diskAt
		if diskErr != nil {
			disksErr = diskErr.Error()
		}
	}
	resp := resourceResponse{
		ResourceSnapshot: &snap,
		UpdatedAt:        max(snap.At, 0),
		Disks:            disks,
		DisksUpdatedAt:   unixOrZero(disksAt),
		DisksError:       disksErr,
	}
	if err != nil {
		resp.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

type resourceHistoryResponse struct {
	Range     string                 `json:"range"`
	From      int64                  `json:"from"`
	To        int64                  `json:"to"`
	UpdatedAt int64                  `json:"updated_at"`
	Points    []system.ResourcePoint `json:"points"`
	Peaks     system.ResourcePeaks   `json:"peaks"`
}

func (s *Server) resourceHistory(w http.ResponseWriter, r *http.Request) {
	if s.Resources == nil {
		writeErr(w, http.StatusServiceUnavailable, "资源采集尚未启动")
		return
	}
	rangeName := r.URL.Query().Get("range")
	rng, ok := parseResourceRange(rangeName)
	if !ok {
		writeErr(w, http.StatusBadRequest, "range 只能是 1m、5m、15m 或 1h")
		return
	}
	if rangeName == "" {
		rangeName = "15m"
	}
	points, peaks, from, to := s.Resources.History(rng)
	if points == nil {
		points = []system.ResourcePoint{}
	}
	updatedAt := int64(0)
	if len(points) > 0 {
		updatedAt = points[len(points)-1].At
	}
	writeJSON(w, http.StatusOK, resourceHistoryResponse{
		Range: rangeName, From: from.Unix(), To: to.Unix(), UpdatedAt: updatedAt,
		Points: points, Peaks: peaks,
	})
}

func parseResourceRange(name string) (time.Duration, bool) {
	switch name {
	case "", "15m":
		return 15 * time.Minute, true
	case "1m":
		return time.Minute, true
	case "5m":
		return 5 * time.Minute, true
	case "1h":
		return time.Hour, true
	default:
		return 0, false
	}
}
