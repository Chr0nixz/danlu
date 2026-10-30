package logs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuneSafe(t *testing.T) {
	s := []byte("训练")
	if got := RuneSafe(s[:4]); string(got) != "训" {
		t.Fatalf("got %q", got)
	}
	if got := RuneSafe(s); string(got) != "训练" {
		t.Fatalf("got %q", got)
	}
}

func TestReadRangeAlignsStart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	os.WriteFile(p, []byte("ab训练cd"), 0o644)
	// 从 "训" 的第二个字节开始读，应跳到 "练"
	data, next, size, err := ReadRange(p, 3, 100)
	if err != nil || string(data) != "练cd" || next != size {
		t.Fatalf("got %q next=%d size=%d err=%v", data, next, size, err)
	}
}

func TestLastLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	os.WriteFile(p, []byte("start\nepoch 1:  10%|#   | 1/10\repoch 1:  50%|#####| 5/10\x1b[0m\r\n\n"), 0o644)
	if got := LastLine(p); got != "epoch 1:  50%|#####| 5/10" {
		t.Fatalf("got %q", got)
	}

	// 跳过 danlu 自己的开始/结束提示行
	os.WriteFile(p, []byte(BannerPrefix+" #3 开始于 x\nval_acc=0.81\n"+BannerPrefix+"结束于 y，退出码 0\n"), 0o644)
	if got := LastLine(p); got != "val_acc=0.81" {
		t.Fatalf("got %q", got)
	}
	os.WriteFile(p, []byte(BannerPrefix+" #3 开始于 x\n"), 0o644)
	if got := LastLine(p); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestErrorLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	cases := map[string]string{
		// 普通 Python 异常，后面跟着 danlu 的结束提示
		"epoch 1: 50%|#####| 5/10\nTraceback (most recent call last):\n  File \"train.py\", line 9, in <module>\n" +
			"\x1b[31mRuntimeError: CUDA out of memory. Tried to allocate 2.00 GiB\x1b[0m\n" + BannerPrefix + "结束，退出码 1\n": "RuntimeError: CUDA out of memory. Tried to allocate 2.00 GiB",
		// torchrun：真正的原因在 ChildFailedError 汇总之前，带 rank 前缀
		"[rank0]: Traceback (most recent call last):\n[rank0]: torch.OutOfMemoryError: CUDA out of memory.\n" +
			"torch.distributed.elastic.multiprocessing.errors.ChildFailedError: \n====\ntrain.py FAILED\n====\n": "[rank0]: torch.OutOfMemoryError: CUDA out of memory.",
		"step 100 loss=0.3\nKilled\n":         "Killed",
		"epoch 3 done\nval_acc=0.81\n":       "",
		"loss: 0.5\nlr: 3e-4\nError: boom\n": "Error: boom",
	}
	for in, want := range cases {
		os.WriteFile(p, []byte(in), 0o644)
		if got := ErrorLine(p); got != want {
			t.Errorf("ErrorLine(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestFindTracker(t *testing.T) {
	cases := map[string]string{
		"wandb: 🚀 View run at \x1b[34m\x1b[4mhttps://wandb.ai/me/proj/runs/abc123\x1b[0m\n": "https://wandb.ai/me/proj/runs/abc123",
		"wandb: View project at https://wandb.ai/me/proj\n":                                  "",
		"swanlab: 🚀 View run at https://swanlab.cn/@me/proj/runs/x9y8\n":                       "https://swanlab.cn/@me/proj/runs/x9y8",
	}
	for in, want := range cases {
		if got := FindTrackerIn([]byte(in)); got != want {
			t.Errorf("FindTrackerIn(%q) = %q，期望 %q", in, got, want)
		}
	}
}
