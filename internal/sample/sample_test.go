package sample

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLatestCachesValueAndError(t *testing.T) {
	n := 0
	s := &Sampler[int]{Name: "测试数据", Every: time.Hour, Stale: time.Hour, Query: func(context.Context) (int, error) {
		n++
		if n == 2 {
			return n, errors.New("坏了")
		}
		return n, nil
	}}
	s.Refresh(context.Background())
	if v, _, err := s.Latest(); v != 1 || err != nil {
		t.Fatalf("第一次: %d %v", v, err)
	}
	s.Refresh(context.Background())
	if v, _, err := s.Latest(); v != 2 || err == nil {
		t.Fatalf("出错时也缓存返回的值和错误: %d %v", v, err)
	}
}

func TestErrorSinceTracksContinuousFailuresAndClears(t *testing.T) {
	queryErr := errors.New("坏了")
	s := &Sampler[int]{Every: time.Hour, Stale: time.Hour, Query: func(context.Context) (int, error) {
		return 1, queryErr
	}}
	s.Refresh(context.Background())
	first := s.ErrorSince()
	if first.IsZero() {
		t.Fatal("采集失败后应该记录起点")
	}
	s.Refresh(context.Background())
	if got := s.ErrorSince(); !got.Equal(first) {
		t.Fatalf("连续失败应该保留起点: got %v, want %v", got, first)
	}
	queryErr = nil
	s.Refresh(context.Background())
	if got := s.ErrorSince(); !got.IsZero() {
		t.Fatalf("采集恢复后应该清除起点: %v", got)
	}
}

func TestErrorSinceForStaleSample(t *testing.T) {
	s := &Sampler[int]{Stale: time.Minute}
	s.since = time.Now().Add(-2 * time.Minute)
	want := s.since.Add(s.Stale)
	if got := s.ErrorSince(); !got.Equal(want) {
		t.Fatalf("卡住的首次采集错误起点 = %v, want %v", got, want)
	}
}

func TestStaleThenQueryErrorKeepsOriginalStart(t *testing.T) {
	s := &Sampler[int]{Stale: time.Minute, Query: func(context.Context) (int, error) {
		return 0, errors.New("采集超时")
	}}
	s.at = time.Now().Add(-2 * time.Minute)
	want := s.ErrorSince()
	s.Refresh(context.Background())
	if got := s.ErrorSince(); !got.Equal(want) {
		t.Fatalf("过期后收到采集错误不应重置起点: got %v, want %v", got, want)
	}
}

func TestStaleWhenQueryHangs(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	s := &Sampler[int]{Name: "GPU 数据", Hint: "（卡住了）", Every: 10 * time.Millisecond, Stale: 50 * time.Millisecond,
		Query: func(context.Context) (int, error) {
			if calls.Add(1) > 1 {
				<-release // 第二次开始卡住
			}
			return 7, nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	time.Sleep(150 * time.Millisecond)
	v, _, err := s.Latest()
	if v != 7 || err == nil || !strings.Contains(err.Error(), "GPU 数据已经") || !strings.HasSuffix(err.Error(), "（卡住了）") {
		t.Fatalf("卡住以后应该报数据过期: %d %v", v, err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("Query 卡住时不应该叠加新的调用，调用了 %d 次", n)
	}
}

func TestStaleWhenFirstQueryHangs(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	s := &Sampler[int]{Name: "GPU 数据", Every: time.Hour, Stale: 20 * time.Millisecond,
		Query: func(context.Context) (int, error) { <-release; return 1, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	time.Sleep(80 * time.Millisecond)
	if _, _, err := s.Latest(); err == nil {
		t.Fatal("第一次采集就卡住，也应该报数据过期")
	}
}

func TestClone(t *testing.T) {
	s := &Sampler[[]int]{Every: time.Hour, Stale: time.Hour,
		Query: func(context.Context) ([]int, error) { return []int{1, 2}, nil },
		Clone: func(v []int) []int { return append([]int(nil), v...) }}
	s.Refresh(context.Background())
	v, _, _ := s.Latest()
	v[0] = 99
	if w, _, _ := s.Latest(); w[0] != 1 {
		t.Fatal("改了返回值不应该影响缓存")
	}
}
