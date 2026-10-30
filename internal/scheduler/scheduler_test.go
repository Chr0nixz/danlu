package scheduler

import (
	"slices"
	"testing"
)

func TestAllocate(t *testing.T) {
	all := []int{0, 1, 2, 3}
	cases := []struct {
		name   string
		busy   map[int]bool
		n      int
		pinned []int
		want   []int
		ok     bool
	}{
		{"取编号最小的空闲卡", map[int]bool{0: true}, 2, nil, []int{1, 2}, true},
		{"空闲卡不够", map[int]bool{0: true, 1: true, 2: true}, 2, nil, nil, false},
		{"指定卡全部空闲", map[int]bool{0: true}, 2, []int{2, 3}, []int{2, 3}, true},
		{"指定卡有一张忙", map[int]bool{3: true}, 2, []int{2, 3}, nil, false},
		{"指定不存在的卡", nil, 1, []int{7}, nil, false},
		{"不需要卡", map[int]bool{0: true, 1: true, 2: true, 3: true}, 0, nil, []int{}, true},
	}
	for _, c := range cases {
		got, ok := Allocate(all, c.busy, c.n, c.pinned)
		if ok != c.ok || (ok && !slices.Equal(got, c.want)) {
			t.Errorf("%s: got %v %v，期望 %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
