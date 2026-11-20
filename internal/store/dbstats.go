package store

import "fmt"

// DBStats 是数据库自身的大小统计，供资源页的"丹炉占用"使用。
// 都是只读 PRAGMA，毫秒级；单连接上可能短暂排在写事务后面（busy_timeout 兜底）。
type DBStats struct {
	PageCount int64 // 数据库页数（WAL 模式下含 WAL 里尚未合并的内容）
	PageSize  int64 // 每页字节数
	FreePages int64 // 空闲页数，VACUUM 可回收
}

func (s *Store) DBStats() (DBStats, error) {
	var st DBStats
	for _, q := range []struct {
		pragma string
		dest   *int64
	}{
		{"PRAGMA page_count", &st.PageCount},
		{"PRAGMA page_size", &st.PageSize},
		{"PRAGMA freelist_count", &st.FreePages},
	} {
		if err := s.db.QueryRow(q.pragma).Scan(q.dest); err != nil {
			return DBStats{}, fmt.Errorf("读取 %s 失败: %w", q.pragma, err)
		}
	}
	return st, nil
}
