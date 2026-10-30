// Package store 是 danlu 的 SQLite 存储层。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type Store struct{ db *sql.DB }

var migrations = []string{
	`CREATE TABLE jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		workdir TEXT NOT NULL,
		conda_env TEXT NOT NULL DEFAULT '',
		cmd TEXT NOT NULL,
		env_json TEXT NOT NULL DEFAULT '{}',
		gpus_req INTEGER NOT NULL DEFAULT 1,
		gpus_pinned TEXT NOT NULL DEFAULT '',
		gpus_assigned TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		master_port INTEGER NOT NULL DEFAULT 0,
		pid INTEGER NOT NULL DEFAULT 0,
		pid_starttime INTEGER NOT NULL DEFAULT 0,
		exit_code INTEGER,
		fail_reason TEXT NOT NULL DEFAULT '',
		cancel_requested INTEGER NOT NULL DEFAULT 0,
		tracker_url TEXT NOT NULL DEFAULT '',
		git_commit TEXT NOT NULL DEFAULT '',
		git_branch TEXT NOT NULL DEFAULT '',
		git_dirty INTEGER NOT NULL DEFAULT 0,
		snapshot_ok INTEGER NOT NULL DEFAULT 0,
		snapshot_note TEXT NOT NULL DEFAULT '',
		rerun_of INTEGER,
		created_at INTEGER NOT NULL,
		started_at INTEGER,
		ended_at INTEGER
	);
	CREATE INDEX jobs_status ON jobs(status);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE login_failures (
		ip TEXT PRIMARY KEY,
		count INTEGER NOT NULL DEFAULT 0,
		locked_until INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE recent_workdirs (
		path TEXT PRIMARY KEY,
		last_used_at INTEGER NOT NULL
	);`,
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单连接：并发量很小，避免 SQLite 写锁冲突
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		if _, err := s.db.Exec(migrations[i]); err != nil {
			return err
		}
		if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return err
		}
	}
	return nil
}

// ---------- jobs ----------

type Job struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Workdir         string            `json:"workdir"`
	CondaEnv        string            `json:"conda_env"`
	Cmd             string            `json:"cmd"`
	Env             map[string]string `json:"env"`
	GPUsReq         int               `json:"gpus_req"`
	GPUsPinned      []int             `json:"gpus_pinned"`
	GPUsAssigned    []int             `json:"gpus_assigned"`
	Status          string            `json:"status"`
	MasterPort      int               `json:"master_port"`
	PID             int               `json:"pid"`
	PIDStart        uint64            `json:"-"`
	ExitCode        *int              `json:"exit_code"`
	FailReason      string            `json:"fail_reason"`
	CancelRequested bool              `json:"cancel_requested"`
	TrackerURL      string            `json:"tracker_url"`
	GitCommit       string            `json:"git_commit"`
	GitBranch       string            `json:"git_branch"`
	GitDirty        bool              `json:"git_dirty"`
	SnapshotOK      bool              `json:"snapshot_ok"`
	SnapshotNote    string            `json:"snapshot_note"`
	RerunOf         *int64            `json:"rerun_of"`
	CreatedAt       int64             `json:"created_at"`
	StartedAt       *int64            `json:"started_at"`
	EndedAt         *int64            `json:"ended_at"`
}

func (j *Job) Finished() bool {
	return j.Status == StatusSucceeded || j.Status == StatusFailed || j.Status == StatusCancelled
}

const jobCols = `id, name, workdir, conda_env, cmd, env_json, gpus_req, gpus_pinned, gpus_assigned,
	status, master_port, pid, pid_starttime, exit_code, fail_reason, cancel_requested, tracker_url,
	git_commit, git_branch, git_dirty, snapshot_ok, snapshot_note, rerun_of, created_at, started_at, ended_at`

type scanner interface{ Scan(dest ...any) error }

func scanJob(r scanner) (*Job, error) {
	var (
		j                      Job
		envJSON, pinned, asgn  string
		exit                   sql.NullInt64
		rerun, started, ended  sql.NullInt64
		pidStart               int64
	)
	err := r.Scan(&j.ID, &j.Name, &j.Workdir, &j.CondaEnv, &j.Cmd, &envJSON, &j.GPUsReq, &pinned, &asgn,
		&j.Status, &j.MasterPort, &j.PID, &pidStart, &exit, &j.FailReason, &j.CancelRequested, &j.TrackerURL,
		&j.GitCommit, &j.GitBranch, &j.GitDirty, &j.SnapshotOK, &j.SnapshotNote, &rerun, &j.CreatedAt, &started, &ended)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	j.PIDStart = uint64(pidStart)
	_ = json.Unmarshal([]byte(envJSON), &j.Env)
	if j.Env == nil {
		j.Env = map[string]string{}
	}
	j.GPUsPinned = ParseInts(pinned)
	j.GPUsAssigned = ParseInts(asgn)
	if exit.Valid {
		v := int(exit.Int64)
		j.ExitCode = &v
	}
	if rerun.Valid {
		j.RerunOf = &rerun.Int64
	}
	if started.Valid {
		j.StartedAt = &started.Int64
	}
	if ended.Valid {
		j.EndedAt = &ended.Int64
	}
	return &j, nil
}

func (s *Store) InsertJob(j *Job) error {
	if j.CreatedAt == 0 {
		j.CreatedAt = time.Now().Unix()
	}
	env, _ := json.Marshal(j.Env)
	res, err := s.db.Exec(`INSERT INTO jobs (name, workdir, conda_env, cmd, env_json, gpus_req, gpus_pinned,
		status, rerun_of, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		j.Name, j.Workdir, j.CondaEnv, j.Cmd, string(env), j.GPUsReq, JoinInts(j.GPUsPinned),
		j.Status, j.RerunOf, j.CreatedAt)
	if err != nil {
		return err
	}
	j.ID, err = res.LastInsertId()
	return err
}

// SaveJob 写回运行期会变化的字段。
func (s *Store) SaveJob(j *Job) error {
	var exit any
	if j.ExitCode != nil {
		exit = *j.ExitCode
	}
	_, err := s.db.Exec(`UPDATE jobs SET gpus_assigned=?, status=?, master_port=?, pid=?, pid_starttime=?,
		exit_code=?, fail_reason=?, cancel_requested=?, tracker_url=?, git_commit=?, git_branch=?, git_dirty=?,
		snapshot_ok=?, snapshot_note=?, started_at=?, ended_at=? WHERE id=?`,
		JoinInts(j.GPUsAssigned), j.Status, j.MasterPort, j.PID, int64(j.PIDStart),
		exit, j.FailReason, j.CancelRequested, j.TrackerURL, j.GitCommit, j.GitBranch, j.GitDirty,
		j.SnapshotOK, j.SnapshotNote, j.StartedAt, j.EndedAt, j.ID)
	return err
}

func (s *Store) GetJob(id int64) (*Job, error) {
	return scanJob(s.db.QueryRow("SELECT "+jobCols+" FROM jobs WHERE id=?", id))
}

// JobsByStatus 按 id 升序返回，排队任务的顺序就是提交顺序。
func (s *Store) JobsByStatus(status string) ([]*Job, error) {
	return s.queryJobs("SELECT "+jobCols+" FROM jobs WHERE status=? ORDER BY id", status)
}

type JobFilter struct {
	Statuses []string
	Query    string
	Limit    int
	Offset   int
}

func (s *Store) ListJobs(f JobFilter) ([]*Job, int, error) {
	var where []string
	var args []any
	if len(f.Statuses) > 0 {
		where = append(where, "status IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Statuses)), ",")+")")
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.Query != "" {
		where = append(where, "(name LIKE ? OR cmd LIKE ? OR workdir LIKE ?)")
		q := "%" + f.Query + "%"
		args = append(args, q, q, q)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM jobs"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	jobs, err := s.queryJobs("SELECT "+jobCols+" FROM jobs"+cond+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, f.Limit, f.Offset)...)
	return jobs, total, err
}

func (s *Store) queryJobs(q string, args ...any) ([]*Job, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func ParseInts(s string) []int {
	out := []int{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if v, err := strconv.Atoi(p); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func JoinInts(v []int) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

// ---------- sessions ----------

func (s *Store) CreateSession(tokenHash string, ttl time.Duration, ip, ua string) error {
	now := time.Now()
	_, err := s.db.Exec("INSERT INTO sessions (token_hash, created_at, expires_at, ip, user_agent) VALUES (?,?,?,?,?)",
		tokenHash, now.Unix(), now.Add(ttl).Unix(), ip, truncate(ua, 300))
	return err
}

func (s *Store) SessionValid(tokenHash string) bool {
	var exp int64
	err := s.db.QueryRow("SELECT expires_at FROM sessions WHERE token_hash=?", tokenHash).Scan(&exp)
	return err == nil && exp > time.Now().Unix()
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return err
}

func (s *Store) DeleteSessionsExcept(keepHash string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE token_hash<>?", keepHash)
	return err
}

func (s *Store) PurgeExpiredSessions() error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix())
	return err
}

// ---------- 登录失败计数 ----------

// LockedUntil 返回该 IP 被锁定到的时间；没有锁定时返回零值。
func (s *Store) LockedUntil(ip string) time.Time {
	var until int64
	if err := s.db.QueryRow("SELECT locked_until FROM login_failures WHERE ip=?", ip).Scan(&until); err != nil {
		return time.Time{}
	}
	if t := time.Unix(until, 0); t.After(time.Now()) {
		return t
	}
	return time.Time{}
}

// RecordFailure 记一次失败，达到 max 次后锁定 lockout，并返回是否刚被锁定。
func (s *Store) RecordFailure(ip string, max int, lockout time.Duration) (bool, error) {
	var count int
	err := s.db.QueryRow("SELECT count FROM login_failures WHERE ip=?", ip).Scan(&count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	count++
	locked := count >= max
	until := int64(0)
	if locked {
		until = time.Now().Add(lockout).Unix()
		count = 0
	}
	_, err = s.db.Exec(`INSERT INTO login_failures (ip, count, locked_until) VALUES (?,?,?)
		ON CONFLICT(ip) DO UPDATE SET count=excluded.count, locked_until=excluded.locked_until`, ip, count, until)
	return locked, err
}

func (s *Store) ResetFailures(ip string) error {
	_, err := s.db.Exec("DELETE FROM login_failures WHERE ip=?", ip)
	return err
}

// ---------- 审计 ----------

type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	IP     string `json:"ip"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}

func (s *Store) Audit(ip, action, detail string) {
	_, _ = s.db.Exec("INSERT INTO audit (ts, ip, action, detail) VALUES (?,?,?,?)",
		time.Now().Unix(), ip, action, truncate(detail, 500))
}

func (s *Store) ListAudit(limit, offset int) ([]AuditEntry, error) {
	rows, err := s.db.Query("SELECT id, ts, ip, action, detail FROM audit ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.IP, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- 最近使用的工作目录 ----------

func (s *Store) TouchWorkdir(path string) {
	_, _ = s.db.Exec(`INSERT INTO recent_workdirs (path, last_used_at) VALUES (?,?)
		ON CONFLICT(path) DO UPDATE SET last_used_at=excluded.last_used_at`, path, time.Now().Unix())
}

func (s *Store) RecentWorkdirs(n int) ([]string, error) {
	rows, err := s.db.Query("SELECT path FROM recent_workdirs ORDER BY last_used_at DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
