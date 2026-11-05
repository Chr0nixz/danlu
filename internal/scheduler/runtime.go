package scheduler

import (
	"context"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/runner"
	"danlu/internal/store"
)

const (
	procInterval     = 30 * time.Second
	profileSaveEvery = 5 * time.Minute
	gpuCoverageGap   = 4 * time.Second
)

// runTarget 区分同一任务重新占位后启动的进程。已结束进程的迟到采样不会归给新进程。
type runTarget struct {
	id       int64
	pid      int
	pidStart uint64
}

type statTarget struct {
	runTarget
	procAfter time.Time
}

type procSample struct {
	at    time.Time
	stats runner.ProcessStats
}

type runStat struct {
	profile             *store.ResourceProfile
	lastSave            time.Time
	gpuAt, gpuCovered   time.Time
	hostAt, hostCovered time.Time
	logKnown            bool
	lastOutput          time.Time
	longestOutputGap    time.Duration
	silentSince         time.Time
	silentDetected      time.Time
}

type RuntimeView struct {
	Profile     *store.ResourceProfile
	SilentSince int64
}

// RuntimeView 读取不可变的采样视图，供 API 使用。
func (m *Manager) RuntimeView(id int64) RuntimeView {
	if views := m.runtimeViews.Load(); views != nil {
		return (*views)[id]
	}
	return RuntimeView{}
}

// sampleProcesses 只读取目标快照，不访问数据库。退出时无需等待系统调用返回。
func (m *Manager) sampleProcesses(ctx context.Context) {
	t := time.NewTicker(time.Second)
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
		previous := m.procSamples.Load()
		next := make(map[runTarget]procSample, len(*targets))
		for _, target := range *targets {
			if ctx.Err() != nil {
				return
			}
			if previous != nil {
				if sample := (*previous)[target.runTarget]; time.Since(sample.at) < procInterval && !sample.at.Before(target.procAfter) {
					next[target.runTarget] = sample
					continue
				}
			}
			stats := m.ex.ProcStats(target.id)
			next[target.runTarget] = procSample{at: time.Now(), stats: stats}
		}
		m.procSamples.Store(&next)
	}
}

func (m *Manager) updateRunStat(j *store.Job, target runTarget, gpus []gpu.GPU, gpuAt time.Time, gpuErr error, procs *map[runTarget]procSample, now time.Time) *runStat {
	stat := m.runStats[j.ID]
	if stat == nil {
		stat = &runStat{profile: j.Profile.Clone(), lastSave: now}
		m.runStats[j.ID] = stat
	}
	if gpuErr != nil {
		stat.gpuCovered = time.Time{}
	} else if j.StartedAt != nil && !gpuAt.Before(time.Unix(*j.StartedAt, 0)) && gpuAt.After(stat.gpuAt) && len(j.GPUsAssigned) > 0 {
		stat.recordGPU(j, gpus, gpuAt)
	}
	if procs != nil {
		if sample := (*procs)[target]; sample.at.After(stat.hostAt) {
			stat.recordHost(sample)
		}
	}
	if stat.profile != nil && now.Sub(stat.lastSave) >= profileSaveEvery {
		j.Profile, stat.lastSave = stat.profile.Clone(), now
		m.save(j)
	}
	return stat
}

func (s *runStat) recordGPU(j *store.Job, gpus []gpu.GPU, at time.Time) {
	s.gpuAt = at
	var totalUtil int
	cards := make([]store.CardProfile, 0, len(j.GPUsAssigned))
	for _, index := range j.GPUsAssigned {
		for _, card := range gpus {
			if card.Index != index {
				continue
			}
			totalUtil += card.Util
			var memory int
			for _, proc := range card.Procs {
				if proc.JobID == j.ID {
					memory += proc.UsedMemMB
				}
			}
			cards = append(cards, store.CardProfile{Index: index, PeakMemMB: memory, TotalMB: card.MemTotalMB})
			break
		}
	}
	if len(cards) != len(j.GPUsAssigned) {
		s.gpuCovered = time.Time{}
		return
	}
	if s.profile == nil {
		s.profile = &store.ResourceProfile{}
	}
	if s.profile.GPU == nil {
		s.profile.GPU = &store.GPUProfile{}
	}
	p := s.profile.GPU
	p.Samples++
	p.AvgUtil += (float64(totalUtil)/float64(len(cards)) - p.AvgUtil) / float64(p.Samples)
	p.CoveredSeconds += coveredSeconds(s.gpuCovered, at, gpuCoverageGap)
	s.gpuCovered = at
	for i, card := range cards {
		for _, prev := range p.Cards {
			if prev.Index == card.Index {
				cards[i].PeakMemMB = max(card.PeakMemMB, prev.PeakMemMB)
				break
			}
		}
	}
	p.Cards = cards
}

func (s *runStat) recordHost(sample procSample) {
	s.hostAt = sample.at
	if !sample.stats.RSSKnown {
		s.hostCovered = time.Time{}
		return
	}
	if s.profile == nil {
		s.profile = &store.ResourceProfile{}
	}
	if s.profile.Host == nil {
		s.profile.Host = &store.HostProfile{}
	}
	p := s.profile.Host
	p.Samples++
	p.PeakRSSBytes = max(p.PeakRSSBytes, sample.stats.RSSBytes)
	p.CoveredSeconds += coveredSeconds(s.hostCovered, sample.at, 2*procInterval)
	s.hostCovered = sample.at
}

func coveredSeconds(previous, next time.Time, gap time.Duration) float64 {
	if delta := next.Sub(previous); !previous.IsZero() && delta > 0 && delta <= gap {
		return delta.Seconds()
	}
	return 0
}
