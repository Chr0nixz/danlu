package settings

import "time"

// StrictKey 是严格限卡开关在 settings 表里的键，值只有 "1" 和没有两种。
const StrictKey = "runtime.strict_gpu"

// SetStrict 写严格限卡开关并让缓存立刻跟上。调用方（接口）负责打开前确认助手可用。
// 只影响之后启动的任务：正在跑的严格任务留在自己的 systemd 服务里，不受开关影响。
func (r *Runtime) SetStrict(on bool) error {
	var err error
	if on {
		err = r.st.SetSetting(StrictKey, "1")
	} else {
		err = r.st.DeleteSettings(StrictKey)
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.cur.Strict, r.at = on, time.Time{}
	r.mu.Unlock()
	return nil
}
