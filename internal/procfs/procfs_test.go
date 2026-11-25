package procfs

import (
	"os"
	"testing"
)

func TestParseStat(t *testing.T) {
	// 进程名里带空格和右括号
	s := "4242 (python (main) x) S 1 4242 4242 0 -1 4194304 100 0 0 0 5 3 0 0 20 0 8 0 987654 123 456"
	st, err := ParseStat(s)
	if err != nil {
		t.Fatal(err)
	}
	if st.PID != 4242 || st.State != 'S' || st.PPID != 1 || st.PGID != 4242 || st.StartTime != 987654 ||
		st.Comm != "python (main) x" || st.UTime != 5 || st.STime != 3 || st.RSSPages != 456 {
		t.Fatalf("解析错误: %+v", st)
	}
}

func TestLookupEnv(t *testing.T) {
	env := []byte("A=1\x00DANLU_JOB_ID=17\x00DANLU_JOB_ID_X=9\x00")
	if v, ok := LookupEnv(env, "DANLU_JOB_ID"); !ok || v != "17" {
		t.Fatalf("got %q %v", v, ok)
	}
	if _, ok := LookupEnv(env, "MISSING"); ok {
		t.Fatal("不应找到")
	}
}

// GPU 上进程的归属：读不到 environ 时按进程在不在、属主是谁区分
func TestClassifyOwnership(t *testing.T) {
	run := "/home/u/.danlu/run"
	env := []byte("A=1\x00DANLU_RUN_DIR=/home/u/.danlu/run/7\x00CUDA_VISIBLE_DEVICES=0,1\x00")
	if o := classify(env, nil, run, -1, true, 1000); o.Job != 7 || o.CVD == nil || *o.CVD != "0,1" || o.Unknown || o.Gone {
		t.Fatalf("本实例任务的进程: %+v", o)
	}
	if o := classify([]byte("A=1\x00"), nil, run, -1, true, 1000); o != (Ownership{}) {
		t.Fatalf("没有标记的进程是外部进程: %+v", o)
	}
	// 正在退出的进程 environ 是空的；僵尸也是
	if o := classify(nil, nil, run, 1000, true, 1000); !o.Unknown || o.Job != 0 {
		t.Fatalf("同一用户、environ 为空的进程应认不出: %+v", o)
	}
	if o := classify(nil, ErrEnvTimeout, run, 1000, true, 1000); !o.Unknown {
		t.Fatalf("读超时应认不出: %+v", o)
	}
	if o := classify(nil, os.ErrPermission, run, 0, true, 1000); o != (Ownership{}) {
		t.Fatalf("别的用户的进程是外部进程: %+v", o)
	}
	if o := classify(nil, os.ErrNotExist, run, -1, false, 1000); !o.Gone {
		t.Fatalf("已经退出的进程应丢掉: %+v", o)
	}
}

func TestEnvReaderOwnProcess(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("没有 /proc")
	}
	var r EnvReader
	b, err := r.Read(os.Getpid())
	if err != nil || len(b) == 0 {
		t.Fatalf("应读到自己的环境: %v", err)
	}
	if len(r.inflight) != 0 {
		t.Fatalf("读完应清掉进行中的记录: %v", r.inflight)
	}
}
