package store

// AlertData 只记录结构化事实。通知端自行生成文案，不转发日志、路径和原始错误。
type AlertData struct {
	Type           string `json:"type"`
	GPUs           []int  `json:"gpus,omitempty"`
	PID            int    `json:"pid,omitempty"`
	AvailableBytes uint64 `json:"available_bytes,omitempty"`
	DataDisk       bool   `json:"data_disk,omitempty"`
	Name           string `json:"name,omitempty"`
	Since          int64  `json:"since,omitempty"`
	Util           *int   `json:"util,omitempty"`
	DState         *int   `json:"d_state,omitempty"`
}
