package procfs

import (
	"os"
	"testing"
)

// 查不到用户名时要退回数字 uid，不能返回空串：面板上的进程属主列会因此整列消失。
func TestOwnerOfUIDFallback(t *testing.T) {
	if got := ownerOfUID("4294967294"); got != "4294967294" {
		t.Fatalf("未知 uid 应该原样返回: %q", got)
	}
	// 第二次走缓存，结果要一致
	if got := ownerOfUID("4294967294"); got != "4294967294" {
		t.Fatalf("缓存后的结果应该一致: %q", got)
	}
}

func TestOwnerSelf(t *testing.T) {
	if got := Owner(os.Getpid()); got == "" {
		t.Fatal("自己的进程应该有属主")
	}
	if got := Owner(-1); got != "" {
		t.Fatalf("不存在的 pid 应返回空: %q", got)
	}
}
