package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
)

func (m *Manager) diskAvailableLocked() (uint64, error) {
	if m.opt.Disks == nil {
		return 0, errors.New("磁盘采集尚未配置")
	}
	disks, _, err := m.opt.Disks.Latest()
	if err != nil {
		return 0, err
	}
	for _, disk := range disks {
		if disk.Data {
			if disk.Error != "" {
				return 0, errors.New(disk.Error)
			}
			return disk.AvailableBytes, nil
		}
	}
	return 0, errors.New("还没有数据目录所在分区的磁盘余量")
}

func (m *Manager) retryDiskPauseLocked() error {
	if m.diskPause == nil {
		return nil
	}
	data, err := json.Marshal(m.diskPause)
	if err != nil {
		return err
	}
	if err := m.st.SetSettingWithEvent(queueStateKey, string(data), m.diskPauseEvent); err != nil {
		return err
	}
	m.diskPause, m.diskPauseEvent = nil, nil
	return nil
}

func (m *Manager) canResumeDiskLocked(current QueueState) error {
	minBytes := m.opt.DiskMinBytes
	if minBytes == 0 {
		return nil
	}
	available, err := m.diskAvailableLocked()
	if err != nil {
		return fmt.Errorf("无法确认磁盘余量，暂不能继续队列: %w", err)
	}
	if current.Kind == "disk" {
		minBytes *= 2
	}
	if available < minBytes {
		return invalid("数据目录所在分区剩余 %.1f GiB，达到 %.1f GiB 后才能继续", float64(available)/(1<<30), float64(minBytes)/(1<<30))
	}
	return nil
}

// 只从缓存读容量，不在调度锁里访问磁盘。已有的手动或快速失败暂停不会被自动清除。
func (m *Manager) checkDiskGuardLocked() error {
	if err := m.retryDiskPauseLocked(); err != nil {
		return err
	}
	if m.opt.DiskMinBytes == 0 && m.opt.Disks == nil {
		return nil
	}
	current, err := m.queueStateLocked()
	if err != nil {
		return err
	}
	if m.opt.DiskMinBytes == 0 {
		if current.Paused && current.Kind == "disk" {
			_, _, err = m.resumeQueueLocked(current)
		}
		return err
	}
	available, err := m.diskAvailableLocked()
	if err != nil {
		return err
	}
	if available < m.opt.DiskMinBytes && !current.Paused {
		reason := fmt.Sprintf("数据目录所在分区仅剩 %.1f GiB（暂停线 %.1f GiB）", float64(available)/(1<<30), float64(m.opt.DiskMinBytes)/(1<<30))
		_, _, err = m.pauseQueueLocked("disk", reason, nil)
	} else if current.Paused && current.Kind == "disk" && available >= m.opt.DiskMinBytes*2 {
		_, _, err = m.resumeQueueLocked(current)
	}
	return err
}
