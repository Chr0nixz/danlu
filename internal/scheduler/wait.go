package scheduler

import "fmt"

// 排队任务在等什么。面板按 Kind 和参数用当前语言合成文字（packages/core 的 waitText）；
// Text() 是中文原文，给旧客户端的 wait_reason 和单测用。
const (
	WaitAhead      = "ahead"       // 前面还有任务
	WaitHeld       = "held"        // 暂缓，等恢复
	WaitScheduling = "scheduling"  // 卡够了，等下一轮调度
	WaitQueueState = "queue_state" // 读不到队列状态
	WaitPaused     = "paused"      // 队列暂停（原因见队列横幅）
	WaitDiskCheck  = "disk_check"  // 磁盘余量还没确认
	WaitGPUData    = "gpu_data"    // GPU 数据不可信
	WaitStrict     = "strict"      // 严格限卡开着但助手不可用
	WaitResLoading = "res_loading" // GPU 预约还没读出来
	WaitGPUs       = "gpus"        // 空闲卡不够
	WaitPinned     = "pinned"      // 指定的卡不空
)

// 挡住队首的那张卡
const (
	BlockReserved   = "reserved"    // 被预约占着
	BlockUnknownMem = "unknown_mem" // 有未知显存占用
	BlockExternal   = "external"    // 有外部进程
	BlockJobProc    = "job_proc"    // 有别的任务跑过来的进程
	// 有认不出归属的进程：和 danlu 同一用户、正在退出或者是僵尸（多半是刚结束的任务没退干净），不告警
	BlockOwnerUnknown = "owner_unknown"
)

type Wait struct {
	Kind    string       `json:"kind"`
	Need    int          `json:"need,omitempty"`
	Free    int          `json:"free,omitempty"`
	Pinned  []int        `json:"pinned,omitempty"`
	Blocker *WaitBlocker `json:"blocker,omitempty"`
	// Detail 是暂停原因或严格限卡的诊断原文（中文），只用于 Text()
	Detail string `json:"-"`
}

type WaitBlocker struct {
	Kind          string `json:"kind"`
	GPU           int    `json:"gpu"`
	ReservationID int64  `json:"reservation_id,omitempty"`
	MemMB         int    `json:"mem_mb,omitempty"`
	PID           int    `json:"pid,omitempty"`
	JobID         int64  `json:"job_id,omitempty"`
}

func (w Wait) Text() string {
	var s string
	switch w.Kind {
	case WaitAhead:
		s = "等待前面的任务"
	case WaitHeld:
		s = "已暂缓"
	case WaitQueueState:
		s = "等待队列状态恢复"
	case WaitPaused:
		s = "队列已暂停：" + w.Detail
	case WaitDiskCheck:
		s = "等待磁盘余量确认"
	case WaitGPUData:
		s = "GPU 数据不可用"
	case WaitStrict:
		s = "严格限卡不可用：" + w.Detail
	case WaitResLoading:
		s = "等待读取 GPU 预约"
	case WaitGPUs:
		s = fmt.Sprintf("等待 %d 张空闲卡（当前 %d 张可用）", w.Need, w.Free)
	case WaitPinned:
		s = fmt.Sprintf("等待指定 GPU %v", w.Pinned)
	default:
		s = "等待调度"
	}
	if b := w.Blocker; b != nil {
		s += "；" + b.Text()
	}
	return s
}

func (b WaitBlocker) Text() string {
	switch b.Kind {
	case BlockReserved:
		return fmt.Sprintf("GPU %d 已被预约 #%d 占用", b.GPU, b.ReservationID)
	case BlockUnknownMem:
		return fmt.Sprintf("GPU %d 有未知显存占用（%d MiB）", b.GPU, b.MemMB)
	case BlockOwnerUnknown:
		return fmt.Sprintf("GPU %d 上有归属不明的进程（PID %d），可能是刚结束的任务还没退出", b.GPU, b.PID)
	case BlockJobProc:
		// 任务自己选了卡、渲染落到了别的卡，或者已结束任务的残留进程
		return fmt.Sprintf("GPU %d 有任务 #%d 的进程（PID %d），这张卡没有分给它", b.GPU, b.JobID, b.PID)
	default:
		return fmt.Sprintf("GPU %d 有外部进程（PID %d）", b.GPU, b.PID)
	}
}
