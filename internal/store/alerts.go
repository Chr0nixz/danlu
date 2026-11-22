package store

// AlertData 只记录结构化事实。通知端自行生成文案，不转发日志、路径和原始错误。
type AlertData struct {
	Type           string `json:"type"`
	GPUs           []int  `json:"gpus,omitempty"`
	PID            int    `json:"pid,omitempty"`
	OtherJob       int64  `json:"other_job,omitempty"` // external：占卡的是本实例另一个任务的进程
	AvailableBytes uint64 `json:"available_bytes,omitempty"`
	DataDisk       bool   `json:"data_disk,omitempty"`
	Name           string `json:"name,omitempty"`
	Since          int64  `json:"since,omitempty"`
	Util           *int   `json:"util,omitempty"`
	DState         *int   `json:"d_state,omitempty"`
	// stray：任务的进程跑到了没分给它的卡上（GPUs），Assigned 是分给它的卡，Reserved 是其中被预约收下的卡。
	// CUDAVisible 是某个计算进程启动时的选卡变量（和分配的不一致时才记，空串表示没设）；Graphics 表示全是图形进程
	Assigned    []int   `json:"assigned,omitempty"`
	Reserved    []int   `json:"reserved,omitempty"`
	CUDAVisible *string `json:"cuda_visible,omitempty"`
	Graphics    bool    `json:"graphics,omitempty"`
	// strict_unavailable：严格限卡开着但 root 助手不可用，Reason 是 strict 包的原因代码
	Reason string `json:"reason,omitempty"`
}
