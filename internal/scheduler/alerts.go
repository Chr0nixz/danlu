package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
	"danlu/internal/strict"
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
	// 越界告警在任务运行期间只发一次：评估阶段的渲染进程时有时无，清掉再出现会反复推送。
	// GPU 数据不可信时也照留，任务结束才清
	runningIDs := make(map[int64]bool, len(running))
	for _, j := range running {
		runningIDs[j.ID] = true
	}
	for key, previous := range m.alerts {
		if previous.Kind == store.EventAlertRaised && strings.HasPrefix(key, "stray:") && previous.JobID != nil && runningIDs[*previous.JobID] {
			e := previous
			desired[key] = &e
		}
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
		// 别的任务跑过来的进程按那个任务合成一条：EGL 渲染一个任务就能有几十个进程，逐个告警会刷屏
		for _, j := range running {
			byPID := map[int][]int{}
			byJob := map[int64]*store.AlertData{}
			for _, g := range gpus {
				if !slices.Contains(j.GPUsAssigned, g.Index) {
					continue
				}
				for _, p := range g.Procs {
					switch {
					case p.JobID == j.ID:
					case p.JobID > 0:
						a := byJob[p.JobID]
						if a == nil {
							a = &store.AlertData{Type: "external", OtherJob: p.JobID, PID: p.PID}
							byJob[p.JobID] = a
						}
						a.PID = min(a.PID, p.PID)
						a.GPUs = append(a.GPUs, g.Index)
					default:
						byPID[p.PID] = append(byPID[p.PID], g.Index)
					}
				}
			}
			id := j.ID
			for pid, cards := range byPID {
				slices.Sort(cards)
				raise(fmt.Sprintf("external:%d:%d", id, pid), &id,
					store.AlertData{Type: "external", PID: pid, GPUs: slices.Compact(cards)})
			}
			for other, a := range byJob {
				slices.Sort(a.GPUs)
				a.GPUs = slices.Compact(a.GPUs)
				raise(fmt.Sprintf("external:%d:job%d", id, other), &id, *a)
			}
		}
		held := m.heldCardsLocked()
		for _, j := range running {
			if a := strayAlert(j, gpus, held); a != nil && desired[fmt.Sprintf("stray:%d", j.ID)] == nil {
				id := j.ID
				raise(fmt.Sprintf("stray:%d", id), &id, *a)
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
		case !m.strictReadyLocked():
			reason = "严格限卡不可用：" + strictReasonText(m.opt.StrictGate)
		case (head.GPUsReq > 0 || len(head.GPUsPinned) > 0) && !m.resReady:
			reason = "等待读取 GPU 预约"
		default:
			var blocked, unknownBlock bool
			reason, blocked, unknownBlock = allocationWait(gpus, running, m.heldCardsLocked(), head)
			if blocked {
				id := head.ID
				kind := "blocked"
				if unknownBlock {
					kind = "blocked_unknown"
				}
				raise(fmt.Sprintf("blocked:%d", id), &id, store.AlertData{Type: kind})
			}
		}
		waitReasons[head.ID] = reason
	}
	// 严格限卡开着、助手查过且不可用：告警一次（启动后还没查完的"正在检查"不算）。助手恢复后自动清除
	if m.opt.Strict && m.opt.StrictGate != nil {
		if st := m.opt.StrictGate.Status(); !st.OK && st.CheckedAt > 0 && st.Reason != strict.ReasonChecking {
			raise("strict_unavailable", nil, store.AlertData{Type: "strict_unavailable", Reason: st.Reason})
		}
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

// allocationWait 写队首的等待原因。held 是预约收下的卡：和任务分到的卡一样算自有占用，
// 卡上的进程多半是用户自己的任务，不写成阻塞原因，也不触发"队首等待外部进程"。
// strictReasonText 是等待原因里的说明，写明怎么修
func strictReasonText(gate StrictGate) string {
	if gate == nil {
		return "这个 danlu 没有配置严格限卡"
	}
	st := gate.Status()
	switch st.Reason {
	case strict.ReasonChecking:
		return "正在检查 root 助手"
	case strict.ReasonNotInstalled:
		return "没有安装 root 助手，在服务器上运行 sudo danlu strict install"
	case strict.ReasonNoSudo:
		return "sudo 没有放行 root 助手，在服务器上重新运行 sudo danlu strict install"
	case strict.ReasonProtocol:
		return "root 助手和 danlu 版本不一致，在服务器上重新运行 sudo danlu strict install"
	case strict.ReasonUnsupported:
		return "只支持 Linux"
	}
	detail := []rune(st.Detail)
	if len(detail) > 120 {
		detail = append(detail[:120], '…')
	}
	return "root 助手出错：" + string(detail)
}

// strayAlert 汇总任务跑到没分给它的卡上的进程，没有时返回 nil。
// 丹炉只能用 CUDA_VISIBLE_DEVICES 告诉任务用哪些卡，拦不住程序自己选卡：
// 启动器或脚本改写了这个变量（deepspeed --num_gpus、accelerate 的 gpu_ids、export），
// 或者是 EGL/OpenGL 渲染（不看这个变量）。告警里带上能分清这两种情况的事实。
func strayAlert(j *store.Job, gpus []gpu.GPU, held map[int]int64) *store.AlertData {
	want := make([]string, len(j.GPUsAssigned))
	for i, g := range j.GPUsAssigned {
		want[i] = strconv.Itoa(g)
	}
	assigned := strings.Join(want, ",")
	var a *store.AlertData
	for _, g := range gpus {
		if slices.Contains(j.GPUsAssigned, g.Index) {
			continue
		}
		for _, p := range g.Procs {
			if p.JobID != j.ID {
				continue
			}
			if a == nil {
				a = &store.AlertData{Type: "stray", PID: p.PID, Assigned: slices.Clone(j.GPUsAssigned), Graphics: true}
			}
			a.PID = min(a.PID, p.PID)
			a.GPUs = append(a.GPUs, g.Index)
			if _, ok := held[g.Index]; ok {
				a.Reserved = append(a.Reserved, g.Index)
			}
			if !p.Graphics {
				a.Graphics = false
				if a.CUDAVisible == nil && p.CUDAVisible != nil && *p.CUDAVisible != assigned {
					v := *p.CUDAVisible
					a.CUDAVisible = &v
				}
			}
		}
	}
	if a != nil {
		slices.Sort(a.GPUs)
		slices.Sort(a.Reserved)
		a.GPUs = slices.Compact(a.GPUs)
		a.Reserved = slices.Compact(a.Reserved)
	}
	return a
}

func allocationWait(gpus []gpu.GPU, running []*store.Job, held map[int]int64, head *store.Job) (string, bool, bool) {
	all := []int{}
	owned := map[int]bool{}
	assigned := map[int64][]int{}
	for _, j := range running {
		assigned[j.ID] = j.GPUsAssigned
		for _, index := range j.GPUsAssigned {
			owned[index] = true
		}
	}
	for index := range held {
		owned[index] = true
	}
	busy := maps.Clone(owned)
	var blocker string
	unknownBlock := false
	for _, g := range gpus {
		all = append(all, g.Index)
		if id, ok := held[g.Index]; ok {
			if blocker == "" && (len(head.GPUsPinned) == 0 || slices.Contains(head.GPUsPinned, g.Index)) {
				blocker = fmt.Sprintf("GPU %d 已被预约 #%d 占用", g.Index, id)
			}
			continue
		}
		if g.UnknownMemMB > 0 {
			busy[g.Index] = true
			if blocker == "" && (len(head.GPUsPinned) == 0 || slices.Contains(head.GPUsPinned, g.Index)) {
				blocker = fmt.Sprintf("GPU %d 有未知显存占用（%d MiB）", g.Index, g.UnknownMemMB)
				unknownBlock = true
			}
		}
		for _, p := range g.Procs {
			if p.JobID == 0 || !slices.Contains(assigned[p.JobID], g.Index) {
				busy[g.Index] = true
				if blocker == "" && (len(head.GPUsPinned) == 0 || slices.Contains(head.GPUsPinned, g.Index)) {
					blocker = fmt.Sprintf("GPU %d 有外部进程（PID %d）", g.Index, p.PID)
					if p.JobID > 0 {
						// 任务自己选了卡、渲染落到了别的卡，或者已结束任务的残留进程
						blocker = fmt.Sprintf("GPU %d 有任务 #%d 的进程（PID %d），这张卡没有分给它", g.Index, p.JobID, p.PID)
					}
				}
			}
		}
	}
	if _, fits := Allocate(all, busy, head.GPUsReq, head.GPUsPinned); fits {
		return "等待调度", false, false
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
	if blocker != "" {
		reason += "；" + blocker
	}
	_, fitsWithoutExternal := Allocate(all, owned, head.GPUsReq, head.GPUsPinned)
	return reason, blocker != "" && fitsWithoutExternal, unknownBlock
}
