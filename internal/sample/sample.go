// Package sample 定时采集一份数据并缓存最新结果。GPU 和主机资源都用它。
package sample

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Sampler 定时调用 Query，缓存最近一次的结果和错误。
//
// 超过 Stale 没有一次采集完成（比如 nvidia-smi 卡在驱动里，信号都杀不掉），
// Latest 就返回错误。调度器和接口据此不再相信这份数据，而不是一直用旧值。
type Sampler[T any] struct {
	Name  string        // 写在错误里的数据名称，比如 "GPU 数据"
	Hint  string        // 数据过期时附在错误后面的提示
	Every time.Duration // 采集间隔
	Stale time.Duration // 超过这么久没有采集完成，就当作数据过期
	Query func(context.Context) (T, error)
	Clone func(T) T // 返回副本，调用方改了也不影响缓存；不需要时留空

	mu    sync.RWMutex
	val   T
	err   error
	at    time.Time // 最近一次采集完成的时间，不管成功失败
	since time.Time // Run 开始的时间：第一次采集就卡住时，用它判断过期
}

// Run 立即采集一次，之后每隔 Every 一次。调用是同步的，Query 卡住时不会叠加新的调用。
func (s *Sampler[T]) Run(ctx context.Context) {
	s.mu.Lock()
	s.since = time.Now()
	s.mu.Unlock()
	s.Refresh(ctx)
	t := time.NewTicker(s.Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Refresh(ctx)
		}
	}
}

// Refresh 采集一次并缓存结果。Query 出错时也缓存它返回的值：GPU 查不到进程时，卡的数据照样能显示。
func (s *Sampler[T]) Refresh(ctx context.Context) {
	v, err := s.Query(ctx)
	s.mu.Lock()
	s.val, s.err, s.at = v, err, time.Now()
	s.mu.Unlock()
}

// Latest 返回最近一次的结果、采集完成的时间和错误。
func (s *Sampler[T]) Latest() (T, time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.val
	if s.Clone != nil {
		v = s.Clone(v)
	}
	ref := s.at
	if ref.IsZero() {
		ref = s.since
	}
	if age := time.Since(ref); !ref.IsZero() && age > s.Stale {
		return v, s.at, fmt.Errorf("%s已经 %d 秒没有更新%s", s.Name, int(age.Seconds()), s.Hint)
	}
	return v, s.at, s.err
}
