package store

import (
	"errors"
)

var ErrPruning = errors.New("任务正在清理，暂不能修改标星")
var ErrProtected = errors.New("任务未结束、已标星或环境记录尚未完成，跳过清理")

func (s *Store) SetStarred(id int64, starred bool) (*Job, error) {
	res, err := s.db.Exec("UPDATE jobs SET starred=? WHERE id=? AND pruning=0", starred, id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		if _, err := s.GetJob(id); err != nil {
			return nil, err
		}
		return nil, ErrPruning
	}
	return s.GetJob(id)
}

func (s *Store) RetentionJobs() ([]*Job, error) {
	return s.queryJobs("SELECT " + jobCols + " FROM jobs ORDER BY ended_at, id")
}

// ClaimPrune 与标星互斥；清理崩溃留下的标记由下一轮持有文件锁的清理器接管。
func (s *Store) ClaimPrune(id int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE jobs SET pruning=1 WHERE id=? AND starred=0 AND pruned_at IS NULL
		AND status IN ('succeeded','failed','cancelled') AND ended_at IS NOT NULL
		AND COALESCE(json_extract(recipe_json,'$.pending'),0)=0`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) FinishPrune(id int64, at *int64) error {
	_, err := s.db.Exec("UPDATE jobs SET pruning=0, pruned_at=? WHERE id=? AND pruning=1", at, id)
	return err
}

// 一次事务只包一次小文件 unlink。先取得写锁，避免包文件被删除的同时新任务开始引用它。
func (s *Store) RemoveUnusedEnv(hash string, remove func() error) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := setSetting(tx, "retention.env_gc", hash); err != nil {
		return false, err
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status='running'
		OR COALESCE(json_extract(recipe_json,'$.pending'),0)=1
		OR json_extract(recipe_json,'$.env_hash')=?`, hash).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if err := remove(); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) EnvInUse(hash string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status='running'
		OR COALESCE(json_extract(recipe_json,'$.pending'),0)=1
		OR json_extract(recipe_json,'$.env_hash')=?`, hash).Scan(&n)
	return n > 0, err
}
