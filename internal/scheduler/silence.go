package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
)

const logInterval = 10 * time.Second

type logSample struct {
	at, modified time.Time
}

// 日志 stat 和进程扫描分别进行，文件系统卡住时调度器也能继续。
func (m *Manager) sampleLogs(ctx context.Context) {
	t := time.NewTicker(logInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		targets := m.runTargets.Load()
		if targets == nil {
			continue
		}
		samples := make(map[runTarget]logSample, len(*targets))
		for _, target := range *targets {
			if ctx.Err() != nil {
				return
			}
			info, err := os.Stat(m.ex.LogPath(target.id))
			sample := logSample{at: time.Now()}
			if err == nil && info.Mode().IsRegular() {
				sample.modified = info.ModTime()
			}
			samples[target.runTarget] = sample
		}
		m.logSamples.Store(&samples)
	}
}

func (s *runStat) observeLog(sample logSample, started *int64, now time.Time, floor time.Duration) {
	s.logKnown = !sample.modified.IsZero() && now.Sub(sample.at) <= 2*logInterval && !sample.modified.After(now)
	if !s.logKnown {
		return
	}
	output := sample.modified
	if started != nil && output.Before(time.Unix(*started, 0)) {
		output = time.Unix(*started, 0)
	}
	if !s.lastOutput.IsZero() {
		if delta := output.Sub(s.lastOutput); delta > 0 {
			s.longestOutputGap = max(s.longestOutputGap, min(delta, time.Duration(1<<63-1)/3))
		} else if delta < 0 {
			s.longestOutputGap = 0
		}
	}
	s.lastOutput = output
	if sample.at.Sub(output) < max(floor, 3*s.longestOutputGap) {
		s.silentSince, s.silentDetected = time.Time{}, time.Time{}
	} else if !s.silentSince.Equal(output) {
		s.silentSince, s.silentDetected = output, now
	}
}

func (m *Manager) silentAlerts(now time.Time, running []*store.Job, gpus []gpu.GPU, gpuOK bool, desired map[string]*store.Event) {
	procs := m.procSamples.Load()
	for _, j := range running {
		if j.PID <= 0 {
			continue
		}
		key := fmt.Sprintf("silent:%d", j.ID)
		previous := m.alerts[key]
		stat := m.runStats[j.ID]
		if stat == nil || !stat.logKnown {
			if previous.Kind == store.EventAlertRaised {
				e := previous
				desired[key] = &e
				if stat != nil && stat.silentSince.IsZero() {
					var data store.AlertData
					if json.Unmarshal(previous.Data, &data) == nil && data.Since > 0 {
						stat.silentSince = time.Unix(data.Since, 0)
					}
				}
			}
			continue
		}
		if stat.silentSince.IsZero() {
			continue
		}
		if previous.Kind == store.EventAlertRaised {
			// 重启期间恢复过输出时，先结掉原来的静默，再记录新的边沿。
			var data store.AlertData
			if json.Unmarshal(previous.Data, &data) != nil || data.Since == stat.silentSince.Unix() {
				e := previous
				desired[key] = &e
			}
			continue
		}
		var sample procSample
		if procs != nil {
			sample = (*procs)[runTarget{j.ID, j.PID, j.PIDStart}]
		}
		fresh := !sample.at.IsZero() && !sample.at.Before(stat.silentDetected) && now.Sub(sample.at) <= procInterval
		if !fresh && now.Sub(stat.silentDetected) < 2*time.Second {
			continue
		}
		data := store.AlertData{Type: "silent", Name: j.Name, Since: stat.silentSince.Unix()}
		if fresh && sample.stats.Count > 0 {
			data.DState = &sample.stats.DState
		}
		if gpuOK && len(j.GPUsAssigned) > 0 {
			var total, count int
			for _, card := range gpus {
				if slices.Contains(j.GPUsAssigned, card.Index) {
					total += card.Util
					count++
				}
			}
			if count == len(j.GPUsAssigned) {
				util := total / count
				data.Util = &util
			}
		}
		body, _ := json.Marshal(data)
		desired[key] = &store.Event{Key: key, JobID: &j.ID, Data: body}
	}
}
