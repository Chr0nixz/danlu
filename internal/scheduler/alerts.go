package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
)

// WaitReasons 不取调度锁：面板每次读任务都会调用，不能等调度器收尾。返回的 map 只读。
func (m *Manager) WaitReasons() map[int64]string {
	if reasons := m.waitReasons.Load(); reasons != nil {
		return *reasons
	}
	return nil
}

// 只读采样缓存、比较边沿并写事件；HTTP 发送完全在通知 goroutine 中完成。
func (m *Manager) checkAlertsLocked(now time.Time) {
	if err := m.updateAlertsLocked(now); err != nil {
		if m.alertError != err.Error() {
			log.Printf("记录告警变化失败: %v", err)
		}
		m.alertError = err.Error()
	} else {
		m.alertError = ""
	}
}

func (m *Manager) updateAlertsLocked(now time.Time) error {
	gpus, gpuAt, gpuErr := m.gpus.Latest()
	running, err := m.jobsByStatus(store.StatusRunning)
	if err != nil {
		return err
	}
	queued, err := m.jobsByStatus(store.StatusQueued)
	if err != nil {
		return err
	}
	queue, queueErr := m.queueStateLocked()
	waitReasons := map[int64]string{}
	// 填完再发布，读取方拿到的 map 不会再被修改。
	defer m.waitReasons.Store(&waitReasons)
	for _, j := range queued {
		waitReasons[j.ID] = "等待前面的任务"
	}
	if !m.alertsReady {
		m.alerts, err = m.st.LatestAlerts()
		if err != nil {
			return err
		}
		m.alertsReady = true
	}
	desired := map[string]*store.Event{}
	keep := func(prefix string) {
		for key, previous := range m.alerts {
			if strings.HasPrefix(key, prefix) && previous.Kind == store.EventAlertRaised {
				e := previous
				desired[key] = &e
			}
		}
	}
	raise := func(key string, jobID *int64, data store.AlertData) {
		b, _ := json.Marshal(data)
		desired[key] = &store.Event{Key: key, JobID: jobID, Data: b}
	}
	if m.opt.SilenceAlert > 0 {
		m.silentAlerts(now, running, gpus, gpuErr == nil && !gpuAt.IsZero(), desired)
	}
	if gpuErr != nil {
		if m.gpuErrorSince.IsZero() {
			m.gpuErrorSince = now
		}
		if source, ok := m.gpus.(interface{ ErrorSince() time.Time }); ok {
			if since := source.ErrorSince(); !since.IsZero() {
				m.gpuErrorSince = since
			}
		}
		keep("gpu_error")
		keep("external:")
		keep("blocked:")
		if now.Sub(m.gpuErrorSince) >= time.Minute {
			raise("gpu_error", nil, store.AlertData{Type: "gpu_error"})
		}
	} else if gpuAt.IsZero() {
		// 启动后的第一轮采样尚未完成，不能据此宣布旧告警已恢复。
		keep("gpu_error")
		keep("external:")
		keep("blocked:")
	} else {
		m.gpuErrorSince = time.Time{}
		// 同一个任务与进程只记一次，多张卡合并到一条事实里。
		for _, j := range running {
			byPID := map[int][]int{}
			for _, g := range gpus {
				if !slices.Contains(j.GPUsAssigned, g.Index) {
					continue
				}
				for _, p := range g.Procs {
					if p.JobID != j.ID {
						byPID[p.PID] = append(byPID[p.PID], g.Index)
					}
				}
			}
			for pid, cards := range byPID {
				slices.Sort(cards)
				id := j.ID
				raise(fmt.Sprintf("external:%d:%d", id, pid), &id,
					store.AlertData{Type: "external", PID: pid, GPUs: slices.Compact(cards)})
			}
		}
	}
	if m.opt.Disks != nil {
		disks, diskAt, diskErr := m.opt.Disks.Latest()
		if diskErr != nil || diskAt.IsZero() {
			keep("disk_low:")
		} else {
			for _, disk := range disks {
				key := "disk_low:" + disk.ID
				if disk.Error != "" {
					if previous := m.alerts[key]; previous.Kind == store.EventAlertRaised {
						e := previous
						desired[key] = &e
					}
				} else if disk.Warn {
					raise(key, nil, store.AlertData{Type: "disk_low", AvailableBytes: disk.AvailableBytes, DataDisk: disk.Data})
				}
			}
		}
	}
	if len(queued) > 0 {
		head := queued[0]
		reason := "等待调度"
		switch {
		case queueErr != nil:
			reason = "等待队列状态恢复"
		case queue.Paused:
			reason = "队列已暂停：" + queue.Reason
		case m.diskError != "":
			reason = "等待磁盘余量确认"
		case (head.GPUsReq > 0 || len(head.GPUsPinned) > 0) && (gpuErr != nil || len(gpus) == 0):
			reason = "GPU 数据不可用"
		default:
			var blocked bool
			reason, blocked = allocationWait(gpus, running, head)
			if blocked {
				id := head.ID
				raise(fmt.Sprintf("blocked:%d", id), &id, store.AlertData{Type: "blocked"})
			}
		}
		waitReasons[head.ID] = reason
	}
	// 未能读到队列状态时不能判断队首是否已经解除阻塞。
	if queueErr != nil {
		keep("blocked:")
	}
	var changes []*store.Event
	for key, e := range desired {
		previous := m.alerts[key]
		if previous.Kind == store.EventAlertRaised {
			continue
		}
		e.Kind, e.TS = store.EventAlertRaised, now.Unix()
		e.DedupeKey = fmt.Sprintf("alert:%s:%d:raised", key, previous.ID)
		changes = append(changes, e)
	}
	for key, previous := range m.alerts {
		if previous.Kind != store.EventAlertRaised || desired[key] != nil {
			continue
		}
		changes = append(changes, &store.Event{Kind: store.EventAlertCleared, Key: key, JobID: previous.JobID,
			TS: now.Unix(), Data: previous.Data, DedupeKey: fmt.Sprintf("alert:%s:%d:cleared", key, previous.ID)})
	}
	if len(changes) == 0 {
		return nil
	}
	// 稳定顺序便于测试和按游标读取；恢复和出现可以在同一轮发生。
	slices.SortFunc(changes, func(a, b *store.Event) int { return strings.Compare(a.Key, b.Key) })
	if err := m.st.AppendEvents(changes...); err != nil {
		return err
	}
	for _, e := range changes {
		m.alerts[e.Key] = *e
	}
	return nil
}

func allocationWait(gpus []gpu.GPU, running []*store.Job, head *store.Job) (string, bool) {
	all := []int{}
	owned := map[int]bool{}
	assigned := map[int64][]int{}
	for _, j := range running {
		assigned[j.ID] = j.GPUsAssigned
		for _, index := range j.GPUsAssigned {
			owned[index] = true
		}
	}
	busy := maps.Clone(owned)
	var external string
	for _, g := range gpus {
		all = append(all, g.Index)
		for _, p := range g.Procs {
			if p.JobID == 0 || !slices.Contains(assigned[p.JobID], g.Index) {
				busy[g.Index] = true
				if external == "" && (len(head.GPUsPinned) == 0 || slices.Contains(head.GPUsPinned, g.Index)) {
					external = fmt.Sprintf("GPU %d 有外部进程（PID %d）", g.Index, p.PID)
				}
			}
		}
	}
	if _, fits := Allocate(all, busy, head.GPUsReq, head.GPUsPinned); fits {
		return "等待调度", false
	}
	free := 0
	for _, index := range all {
		if !busy[index] {
			free++
		}
	}
	reason := fmt.Sprintf("等待 %d 张空闲卡（当前 %d 张可用）", head.GPUsReq, free)
	if len(head.GPUsPinned) > 0 {
		reason = fmt.Sprintf("等待指定 GPU %v", head.GPUsPinned)
	}
	if external != "" {
		reason += "；" + external
	}
	_, fitsWithoutExternal := Allocate(all, owned, head.GPUsReq, head.GPUsPinned)
	return reason, external != "" && fitsWithoutExternal
}
