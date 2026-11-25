package notify

import (
	"fmt"
	"regexp"
)

// 推送文案目录：zh-CN 是源语言（与历史推送文字一致），en 镜像，键集对齐由单测保证。
// 模板用 {name} 占位（和前端目录同一套写法）；英文用单位式/中性措辞避开单复数变化。
// 语言来自 ui.notify_lang 偏好（Service.Lang），发送时合成，切换对未发批次也生效。
var messages = map[string]map[string]string{
	"zh-CN": {
		"title.finished":             "任务结束",
		"title.succeeded":            "任务完成",
		"title.cancelled":            "任务已取消",
		"title.failed":               "任务失败",
		"title.drained":              "队列已跑完",
		"title.paused":               "队列暂停",
		"title.gpu_recovered":        "GPU 已恢复",
		"title.gpu_error":            "GPU 数据不可用",
		"title.disk_low":             "磁盘余量不足",
		"title.external":             "训练 GPU 被外部占用",
		"title.external_job":         "训练 GPU 上有别的任务的进程",
		"title.stray":                "任务用了没分给它的 GPU",
		"title.strict_unavailable":   "严格限卡不可用",
		"title.blocked_unknown":      "队首等待未知显存",
		"title.blocked":              "队首等待外部进程",
		"title.silent":               "任务无新输出",
		"title.reservation_ready":    "GPU 预约就绪",
		"title.reservation_expiring": "GPU 预约即将到期",
		"title.test":                 "测试推送",

		"pause.disk":               "数据分区空间不足，已暂停启动新任务。",
		"pause.fail_fast":          "{item}。任务启动后很快失败，已暂停启动后续任务，请检查后继续队列。",
		"pause.fail_fast_plain":    "任务 #{id} 快速失败，已暂停启动后续任务，请检查后继续队列。",
		"gpu.recovered":            "nvidia-smi 已恢复，GPU 数据可以正常读取。",
		"gpu.error":                "nvidia-smi 已连续出错超过 60 秒，请检查 GPU 或驱动。",
		"disk.low_data":            "监测到的数据分区仅剩 {free} GiB 可用空间。",
		"disk.low_disk":            "监测到的磁盘分区仅剩 {free} GiB 可用空间。",
		"external.body":            "任务 #{id} 使用的 GPU {gpus} 出现外部进程 PID {pid}。",
		"external.job_body":        "任务 #{id} 使用的 GPU {gpus} 上出现了任务 #{other} 的进程，那个任务跑到了没分给它的卡上。",
		"stray.body":               "任务 #{id} 的进程在 GPU {gpus} 上，分给它的是 GPU {assigned}。",
		"stray.body_none":          "任务 #{id} 没分到 GPU，进程却在 GPU {gpus} 上。",
		"stray.reserved":           "其中 GPU {reserved} 已被预约。",
		"stray.cvd":                "进程的 CUDA_VISIBLE_DEVICES 指向 GPU {cvd}，启动器、框架或脚本改写了它（如 deepspeed --num_gpus、accelerate 的 gpu_ids、按 nvidia-smi/NVML 的编号选卡、脚本里的 export）。",
		"stray.cvd_unset":          "进程没有 CUDA_VISIBLE_DEVICES，启动器或脚本把它去掉了。",
		"stray.graphics":           "这些是图形渲染进程（EGL/OpenGL），不受 CUDA_VISIBLE_DEVICES 限制，要在程序里指定渲染用的卡。",
		"strict.body":              "严格限卡开着，但 root 助手不可用，排队的任务暂停启动。",
		"strict.fix_not_installed": "在服务器上运行 sudo danlu strict install 安装，或在面板设置里关掉严格限卡。",
		"strict.fix_no_sudo":       "sudo 没有放行助手：在服务器上重新运行 sudo danlu strict install，或在面板设置里关掉严格限卡。",
		"strict.fix_protocol":      "助手和 danlu 版本不一致：在服务器上重新运行 sudo danlu strict install。",
		"strict.fix_other":         "打开面板「设置 → 调度」查看原因。",
		"blocked.unknown_body":     "任务 #{id} 正在等待进程列表无法解释的 GPU 显存释放。",
		"blocked.body":             "任务 #{id} 正在等待外部进程释放 GPU。",
		"silent.body":              "#{id} {name} 已 {duration}没有新输出。",
		"silent.util":              "GPU 平均利用率 {util}%。",
		"silent.dstate":            "有 {n} 个进程处于 D 状态，可能在等待存储。",

		// 有无用途备注各一句：中文"#3 已就绪"留空格，"（备注）已就绪"不留，拼名字做不到两全
		"reservation.ready":         "预约 #{id} 已就绪：GPU {gpus}。",
		"reservation.ready_note":    "预约 #{id}（{note}）已就绪：GPU {gpus}。",
		"reservation.ready_auto":    "{duration}后自动释放。",
		"reservation.ready_manual":  "手动释放前一直保留。",
		"reservation.expiring":      "预约 #{id} 的 GPU {gpus} 约 {duration}后自动释放，需要的话在面板上延长。",
		"reservation.expiring_note": "预约 #{id}（{note}）的 GPU {gpus} 约 {duration}后自动释放，需要的话在面板上延长。",

		"test.body": "丹炉手机推送已连通。",

		"line.succeeded": "#{id} {name}：成功，用时 {duration}",
		"line.cancelled": "#{id} {name}：已取消，用时 {duration}",
		"line.failed":    "#{id} {name}：{reason}",
		"batch.more":     "另有 {n} 个任务{verb}，请打开面板查看。",
		"drain.summary":  "{total} 个任务，{ok} 成功，{failed} 失败，{cancelled} 取消。合计任务用时 {duration}。",
		"drain.more":     "另有 {n} 个任务失败，请打开面板查看。",
		"verb.finished":  "结束",
		"verb.failed":    "失败",

		"fail.cuda_oom":               "CUDA 显存不足",
		"fail.host_oom":               "主机内存不足",
		"fail.host_oom_worker":        "主机内存不足",
		"fail.host_oom_guess":         "可能是主机内存不足",
		"fail.nccl":                   "NCCL 通信超时或出错",
		"fail.exception":              "Python 异常",
		"fail.command":                "命令或路径出错",
		"fail.command_not_found":      "命令没找到",
		"fail.command_not_executable": "命令无法执行",
		"fail.command_conda":          "conda 环境激活失败",
		"fail.command_workdir":        "无法进入工作目录",
		"fail.reboot":                 "服务器重启，任务中断",
		"fail.killed":                 "进程被外部终止",
		"fail.start":                  "任务启动失败",
		"fail.signal":                 "进程被信号终止",
		"fail.exit":                   "退出码 {code}",
		"fail.unknown":                "任务异常结束",

		"duration.hm": "{h} 小时 {m} 分钟",
		"duration.m":  "{n} 分钟",
		"duration.s":  "{n} 秒",
		"rough.h":     "{n} 小时",
		"rough.m":     "{n} 分钟",
		"rough.s":     "{n} 秒",
	},
	"en": {
		"title.finished":             "Jobs finished",
		"title.succeeded":            "Jobs succeeded",
		"title.cancelled":            "Jobs cancelled",
		"title.failed":               "Jobs failed",
		"title.drained":              "Queue drained",
		"title.paused":               "Queue paused",
		"title.gpu_recovered":        "GPU recovered",
		"title.gpu_error":            "GPU data unavailable",
		"title.disk_low":             "Low disk space",
		"title.external":             "Training GPU held by an external process",
		"title.external_job":         "Another job's process on a training GPU",
		"title.stray":                "Job using GPUs not assigned to it",
		"title.strict_unavailable":   "Strict GPU isolation unavailable",
		"title.blocked_unknown":      "Queue head waiting on unexplained GPU memory",
		"title.blocked":              "Queue head waiting on an external process",
		"title.silent":               "Job has no new output",
		"title.reservation_ready":    "GPU reservation ready",
		"title.reservation_expiring": "GPU reservation expiring soon",
		"title.test":                 "Test push",

		"pause.disk":               "The data partition is low on space; launching new jobs is paused.",
		"pause.fail_fast":          "{item}. The job failed soon after starting; launching further jobs is paused. Check it and resume the queue.",
		"pause.fail_fast_plain":    "Job #{id} failed quickly; launching further jobs is paused. Check it and resume the queue.",
		"gpu.recovered":            "nvidia-smi recovered; GPU data can be read again.",
		"gpu.error":                "nvidia-smi has been failing for over 60 seconds. Check the GPU or driver.",
		"disk.low_data":            "The monitored data partition has only {free} GiB free.",
		"disk.low_disk":            "The monitored disk partition has only {free} GiB free.",
		"external.body":            "An external process (PID {pid}) appeared on GPU {gpus} used by job #{id}.",
		"external.job_body":        "A process of job #{other} appeared on GPU {gpus} used by job #{id}; that job ran on a card not assigned to it.",
		"stray.body":               "Processes of job #{id} are on GPU {gpus}; it was assigned GPU {assigned}.",
		"stray.body_none":          "Job #{id} was assigned no GPU, but its processes are on GPU {gpus}.",
		"stray.reserved":           "Held by a reservation: GPU {reserved}.",
		"stray.cvd":                "The process's CUDA_VISIBLE_DEVICES points to GPU {cvd}: the launcher, framework or script rewrote it (e.g. deepspeed --num_gpus, accelerate gpu_ids, picking GPUs by nvidia-smi/NVML index, an export in the script).",
		"stray.cvd_unset":          "The process has no CUDA_VISIBLE_DEVICES: the launcher or script removed it.",
		"stray.graphics":           "These are graphics (EGL/OpenGL) processes, which ignore CUDA_VISIBLE_DEVICES; choose the render GPU in the program.",
		"strict.body":              "Strict GPU isolation is on but its root helper is unavailable; queued jobs are not being started.",
		"strict.fix_not_installed": "Run sudo danlu strict install on the server, or turn strict isolation off in the panel settings.",
		"strict.fix_no_sudo":       "sudo does not allow the helper: run sudo danlu strict install again on the server, or turn strict isolation off in the panel settings.",
		"strict.fix_protocol":      "The helper and danlu versions differ: run sudo danlu strict install again on the server.",
		"strict.fix_other":         "Open Settings → Scheduling in the panel to see why.",
		"blocked.unknown_body":     "Job #{id} is waiting for GPU memory that the process list cannot explain to be freed.",
		"blocked.body":             "Job #{id} is waiting for an external process to release the GPU.",
		"silent.body":              "#{id} {name} has had no new output for {duration}.",
		"silent.util":              "Average GPU utilization {util}%.",
		"silent.dstate":            "Processes in D state: {n}, possibly waiting on storage.",

		"reservation.ready":         "Reservation #{id} is ready: GPU {gpus}.",
		"reservation.ready_note":    "Reservation #{id} ({note}) is ready: GPU {gpus}.",
		"reservation.ready_auto":    "Auto-releases in {duration}.",
		"reservation.ready_manual":  "Held until released manually.",
		"reservation.expiring":      "GPU {gpus} of reservation #{id} will auto-release in about {duration}; extend it in the panel if needed.",
		"reservation.expiring_note": "GPU {gpus} of reservation #{id} ({note}) will auto-release in about {duration}; extend it in the panel if needed.",

		"test.body": "Danlu phone push is connected.",

		"line.succeeded": "#{id} {name}: succeeded in {duration}",
		"line.cancelled": "#{id} {name}: cancelled after {duration}",
		"line.failed":    "#{id} {name}: {reason}",
		"batch.more":     "{n} more {verb} — open the panel to see them.",
		"drain.summary":  "Jobs: {total} — {ok} succeeded, {failed} failed, {cancelled} cancelled. Total job time {duration}.",
		"drain.more":     "{n} more failed — open the panel to see them.",
		"verb.finished":  "finished",
		"verb.failed":    "failed",

		"fail.cuda_oom":               "CUDA out of memory",
		"fail.host_oom":               "Host out of memory",
		"fail.host_oom_worker":        "Host out of memory (OOM kill on this host while running)",
		"fail.host_oom_guess":         "Possibly host out of memory",
		"fail.nccl":                   "NCCL communication timeout or error",
		"fail.exception":              "Python exception",
		"fail.command":                "Command or path error",
		"fail.command_not_found":      "Command not found",
		"fail.command_not_executable": "Command not executable",
		"fail.command_conda":          "Could not activate the conda env",
		"fail.command_workdir":        "Could not enter the working directory",
		"fail.reboot":                 "Server rebooted; job interrupted",
		"fail.killed":                 "Process terminated externally",
		"fail.start":                  "Job failed to start",
		"fail.signal":                 "Process terminated by signal",
		"fail.exit":                   "exit code {code}",
		"fail.unknown":                "Job ended abnormally",

		"duration.hm": "{h} h {m} min",
		"duration.m":  "{n} min",
		"duration.s":  "{n} s",
		"rough.h":     "{n} h",
		"rough.m":     "{n} min",
		"rough.s":     "{n} s",
	},
}

var placeholderRe = regexp.MustCompile(`\{(\w+)\}`)

// lang 返回当前生效的推送语言；未设置或非法值按中文。
func (s *Service) lang() string {
	if s.Lang != nil {
		if l := s.Lang(); l == "en" {
			return "en"
		}
	}
	return "zh-CN"
}

// concatSentences 拼接两句正文：中文句间不留空格，英文留一个。
func (s *Service) concatSentences(a, b string) string {
	if s.lang() == "en" {
		return a + " " + b
	}
	return a + b
}

// msg 取当前语言的文案并插值；缺键回退中文，再缺返回键名（单测会抓）。
func (s *Service) msg(key string, params map[string]any) string {
	l := s.lang()
	tpl, ok := messages[l][key]
	if !ok {
		tpl = messages["zh-CN"][key]
	}
	if tpl == "" {
		return key
	}
	if len(params) == 0 {
		return tpl
	}
	return placeholderRe.ReplaceAllStringFunc(tpl, func(m string) string {
		if v, ok := params[m[1:len(m)-1]]; ok {
			return fmt.Sprint(v)
		}
		return m
	})
}
