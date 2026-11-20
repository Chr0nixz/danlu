package store

import (
	"strings"
	"testing"
)

func TestDBStats(t *testing.T) {
	s := openTest(t)
	st, err := s.DBStats()
	if err != nil {
		t.Fatal(err)
	}
	if st.PageCount <= 0 || st.PageSize < 512 || st.PageSize&(st.PageSize-1) != 0 {
		t.Fatalf("新库的统计不合理: %+v", st)
	}
	if st.FreePages < 0 || st.FreePages > st.PageCount {
		t.Fatalf("空闲页数不合理: %+v", st)
	}

	// 写入一批数据后页数应增长
	before := st.PageCount
	for i := range 200 {
		if _, err := s.db.Exec("INSERT INTO audit (ts, ip, action, detail) VALUES (?,?,?,?)",
			int64(i), "127.0.0.1", "test", strings.Repeat("x", 400)); err != nil {
			t.Fatal(err)
		}
	}
	st, err = s.DBStats()
	if err != nil {
		t.Fatal(err)
	}
	if st.PageCount <= before {
		t.Fatalf("写入后页数没有增长: %d → %d", before, st.PageCount)
	}
}
