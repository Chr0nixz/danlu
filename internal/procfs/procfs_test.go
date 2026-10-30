package procfs

import "testing"

func TestParseStat(t *testing.T) {
	// 进程名里带空格和右括号
	s := "4242 (python (main) x) S 1 4242 4242 0 -1 4194304 100 0 0 0 5 3 0 0 20 0 8 0 987654 123 456"
	st, err := ParseStat(s)
	if err != nil {
		t.Fatal(err)
	}
	if st.PID != 4242 || st.State != 'S' || st.PPID != 1 || st.PGID != 4242 || st.StartTime != 987654 {
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
