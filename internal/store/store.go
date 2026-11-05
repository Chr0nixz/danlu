// Package store 是 danlu 的 SQLite 存储层。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	`CREATE TABLE device_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER,
		last_ip TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX jobs_ended ON jobs(ended_at);`,
	`ALTER TABLE jobs ADD COLUMN inherit_bashrc INTEGER NOT NULL DEFAULT 0;`,
	// 占位时记下的开机标识：任务没留下退出码就没了时，用它区分"服务器重启"和"进程被外部终止"
	`ALTER TABLE jobs ADD COLUMN boot_id TEXT NOT NULL DEFAULT '';`,
	// 运行中会变的状态（密码哈希等）。config.toml 只放手写的静态配置，服务进程从不写它
	`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	);`,
	// 状态和事件在同一事务里写入；dedupe_key 让写库重试不会重复记同一件事。
	`ALTER TABLE jobs ADD COLUMN notes TEXT NOT NULL DEFAULT '';
	UPDATE jobs SET
		notes = CASE WHEN instr(fail_reason, '；已清理 ') > 0
			THEN substr(fail_reason, instr(fail_reason, '；已清理 ') + 1) ELSE fail_reason END,
		fail_reason = CASE WHEN instr(fail_reason, '；已清理 ') > 0
			THEN substr(fail_reason, 1, instr(fail_reason, '；已清理 ') - 1) ELSE '' END
	WHERE fail_reason LIKE '已清理 % 个残留进程' OR fail_reason LIKE '%；已清理 % 个残留进程';
	CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		kind TEXT NOT NULL,
		key TEXT NOT NULL DEFAULT '',
		job_id INTEGER,
		data TEXT NOT NULL DEFAULT '{}',
		dedupe_key TEXT NOT NULL UNIQUE
	);
	CREATE INDEX events_ts ON events(ts);
	CREATE INDEX events_job ON events(job_id, id);`,
	`ALTER TABLE jobs ADD COLUMN fail_kind TEXT NOT NULL DEFAULT '';
	ALTER TABLE jobs ADD COLUMN ext_seen INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE jobs ADD COLUMN oom_base INTEGER NOT NULL DEFAULT -1;
	ALTER TABLE jobs ADD COLUMN traceback_offset INTEGER;`,
	`ALTER TABLE jobs ADD COLUMN recipe_json TEXT NOT NULL DEFAULT '{}';`,
	`ALTER TABLE jobs ADD COLUMN output_dir TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE jobs ADD COLUMN starred INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE jobs ADD COLUMN pruned_at INTEGER;
	ALTER TABLE jobs ADD COLUMN pruning INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE jobs ADD COLUMN code_from INTEGER;
	CREATE INDEX jobs_snapshot_users ON jobs(code_from)
		WHERE code_from IS NOT NULL AND snapshot_ok=0 AND status IN ('queued','running');`,
	`CREATE TABLE totp_used_steps (step INTEGER PRIMARY KEY);`,
	`ALTER TABLE jobs ADD COLUMN memo TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE jobs ADD COLUMN profile_json TEXT NOT NULL DEFAULT 'null';`,
	`ALTER TABLE jobs ADD COLUMN notify_done INTEGER NOT NULL DEFAULT 0;`,
}

// keepBackups 是升级前备份最多保留的份数。
const keepBackups = 5

// Open 打开数据库并执行迁移。要升级表结构时，先把旧库备份到数据库所在目录的 backup/ 下。
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单连接：并发量很小，避免 SQLite 写锁冲突
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(filepath.Join(filepath.Dir(path), "backup")); err != nil {
		db.Close()
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(backupDir string) error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("数据库是新版本的 danlu 建的（版本 %d，这个程序只认识到 %d），请换回新版本的程序", v, len(migrations))
	}
	if v > 0 && v < len(migrations) {
		if err := s.backup(backupDir, v); err != nil {
			return fmt.Errorf("升级前备份数据库失败: %w", err)
		}
	}
	for i := v; i < len(migrations); i++ {
		// 迁移和版本号放在同一个事务里：要么都生效，要么都不生效，不会留下改了一半的库
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("第 %d 次迁移失败: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// backup 用 VACUUM INTO 生成一份一致的副本（WAL 里还没合并的内容也在里面），只保留最近 keepBackups 份。
func (s *Store) backup(dir string, version int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// 文件名以时间开头，按名字排序就是按时间排序
	dst := filepath.Join(dir, fmt.Sprintf("danlu-%s-v%d.db", time.Now().Format("20060102-150405.000000000"), version))
	if _, err := s.db.Exec("VACUUM INTO ?", dst); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "danlu-*-v*.db"))
	sort.Strings(old)
	for len(old) > keepBackups {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	return nil
}

// ---------- 运行时设置 ----------

// GetSetting 读一项运行时设置，没有这一项时返回 ErrNotFound。
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now().Unix())
	return err
}

// SetSettingIfAbsentAndClaimTOTP 同一事务中启用两步验证并登记确认时用过的时间窗。
func (s *Store) SetSettingIfAbsentAndClaimTOTP(key, value string, step int64) (bool, error) {
	if key == "" {
		return false, errors.New("运行状态键不能为空")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?,?,?) ON CONFLICT(key) DO NOTHING`,
		key, value, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.Exec("INSERT INTO totp_used_steps (step) VALUES (?)", step); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ClaimTOTPWindow(step int64) (bool, error) {
	res, err := s.db.Exec("INSERT INTO totp_used_steps (step) VALUES (?) ON CONFLICT(step) DO NOTHING", step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) DeleteSettings(keys ...string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err := tx.Exec("DELETE FROM settings WHERE key=?", key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- jobs ----------

type Job struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Workdir         string            `json:"workdir"`
	CondaEnv        string            `json:"conda_env"`
	Cmd             string            `json:"cmd"`
	Env             map[string]string `json:"env"`
	InheritBashrc   bool              `json:"inherit_bashrc"`
	GPUsReq         int               `json:"gpus_req"`
	GPUsPinned      []int             `json:"gpus_pinned"`
	GPUsAssigned    []int             `json:"gpus_assigned"`
	Status          string            `json:"status"`
	MasterPort      int               `json:"master_port"`
	PID             int               `json:"pid"`
	PIDStart        uint64            `json:"-"`
	BootID          string            `json:"-"` // 占位时的开机标识
	ExitCode        *int              `json:"exit_code"`
	FailReason      string            `json:"fail_reason"`
	FailKind        string            `json:"fail_kind"`
	Notes           string            `json:"notes"`
	Memo            string            `json:"memo,omitempty"`
	NotifyDone      bool              `json:"notify_done"`
	ExtSeen         bool              `json:"ext_seen"`
	OOMBase         int64             `json:"-"`
	TracebackOffset *int64            `json:"-"`
	Recipe          *Recipe           `json:"recipe,omitempty"`
	Profile         *ResourceProfile  `json:"profile,omitempty"`
	OutputDir       string            `json:"output_dir,omitempty"`
	Starred         bool              `json:"starred"`
	PrunedAt        *int64            `json:"pruned_at"`
	Pruning         bool              `json:"-"`
	CancelRequested bool              `json:"cancel_requested"`
	TrackerURL      string            `json:"tracker_url"`
	GitCommit       string            `json:"git_commit"`
	GitBranch       string            `json:"git_branch"`
	GitDirty        bool              `json:"git_dirty"`
	SnapshotOK      bool              `json:"snapshot_ok"`
	SnapshotNote    string            `json:"snapshot_note"`
	RerunOf         *int64            `json:"rerun_of"`
	CodeFrom        *int64            `json:"code_from,omitempty"`
	CreatedAt       int64             `json:"created_at"`
	StartedAt       *int64            `json:"started_at"`
	EndedAt         *int64            `json:"ended_at"`
}

type Recipe struct {
	EnvHash      string         `json:"env_hash,omitempty"`
	EnvError     string         `json:"env_error,omitempty"`
	Pending      bool           `json:"pending,omitempty"`
	Machine      *RecipeMachine `json:"machine,omitempty"`
	MachineError string         `json:"machine_error,omitempty"`
}

type RecipeMachine struct {
	Hostname      string      `json:"hostname,omitempty"`
	DriverVersion string      `json:"driver_version,omitempty"`
	CUDAVersion   string      `json:"cuda_version,omitempty"`
	GPUs          []RecipeGPU `json:"gpus,omitempty"`
}

type RecipeGPU struct {
	Index int    `json:"index"`
	UUID  string `json:"uuid,omitempty"`
	Name  string `json:"name,omitempty"`
}

func (j *Job) Finished() bool {
	return j.Status == StatusSucceeded || j.Status == StatusFailed || j.Status == StatusCancelled
}

const jobCols = `id, name, workdir, conda_env, cmd, env_json, gpus_req, gpus_pinned, gpus_assigned,
	status, master_port, pid, pid_starttime, exit_code, fail_reason, cancel_requested, tracker_url,
	git_commit, git_branch, git_dirty, snapshot_ok, snapshot_note, rerun_of, created_at, started_at, ended_at,
	inherit_bashrc, boot_id, notes, fail_kind, ext_seen, oom_base, traceback_offset, recipe_json, output_dir, starred, pruned_at, pruning, code_from, memo, profile_json, notify_done`

type scanner interface{ Scan(dest ...any) error }

func scanJob(r scanner) (*Job, error) {
	var (
		j                                            Job
		envJSON, pinned, asgn, recipeJSON, outputDir string
		exit                                         sql.NullInt64
		rerun, started, ended, traceback             sql.NullInt64
		pidStart                                     int64
		pruned, codeFrom                             sql.NullInt64
		profileJSON                                  string
	)
	err := r.Scan(&j.ID, &j.Name, &j.Workdir, &j.CondaEnv, &j.Cmd, &envJSON, &j.GPUsReq, &pinned, &asgn,
		&j.Status, &j.MasterPort, &j.PID, &pidStart, &exit, &j.FailReason, &j.CancelRequested, &j.TrackerURL,
		&j.GitCommit, &j.GitBranch, &j.GitDirty, &j.SnapshotOK, &j.SnapshotNote, &rerun, &j.CreatedAt, &started, &ended,
		&j.InheritBashrc, &j.BootID, &j.Notes, &j.FailKind, &j.ExtSeen, &j.OOMBase, &traceback, &recipeJSON, &outputDir, &j.Starred, &pruned, &j.Pruning, &codeFrom, &j.Memo, &profileJSON, &j.NotifyDone)
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
	if codeFrom.Valid {
		j.CodeFrom = &codeFrom.Int64
	}
	if started.Valid {
		j.StartedAt = &started.Int64
	}
	if ended.Valid {
		j.EndedAt = &ended.Int64
	}
	if traceback.Valid {
		j.TracebackOffset = &traceback.Int64
	}
	var recipe Recipe
	if err := json.Unmarshal([]byte(recipeJSON), &recipe); err != nil {
		return nil, fmt.Errorf("任务 #%d 的配方 JSON 无效: %w", j.ID, err)
	}
	if recipe.EnvHash != "" || recipe.EnvError != "" || recipe.Pending || recipe.Machine != nil || recipe.MachineError != "" {
		j.Recipe = &recipe
	}
	j.OutputDir = outputDir
	var profile *ResourceProfile
	if json.Unmarshal([]byte(profileJSON), &profile) == nil {
		j.Profile = profile
	}
	if pruned.Valid {
		j.PrunedAt = &pruned.Int64
	}
	return &j, nil
}

var ErrSnapshotUnavailable = errors.New("源任务的代码快照不可用")

func (s *Store) InsertJob(j *Job) error {
	if j.CreatedAt == 0 {
		j.CreatedAt = time.Now().Unix()
	}
	env, _ := json.Marshal(j.Env)
	// 在同一条写语句里核对源快照和登记引用，与清理器的认领互斥。
	res, err := s.db.Exec(`INSERT INTO jobs (name, workdir, conda_env, cmd, env_json, gpus_req, gpus_pinned,
		status, rerun_of, created_at, inherit_bashrc, code_from, memo, notify_done)
		SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE ? IS NULL OR EXISTS (
			SELECT 1 FROM jobs WHERE id=? AND snapshot_ok=1 AND pruned_at IS NULL AND pruning=0
				AND (pid>0 OR ended_at IS NOT NULL)
		)`,
		j.Name, j.Workdir, j.CondaEnv, j.Cmd, string(env), j.GPUsReq, JoinInts(j.GPUsPinned),
		j.Status, j.RerunOf, j.CreatedAt, j.InheritBashrc, j.CodeFrom, j.Memo, j.NotifyDone, j.CodeFrom, j.CodeFrom)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrSnapshotUnavailable
	}
	j.ID, err = res.LastInsertId()
	return err
}

// SaveJob 写回运行期会变化的字段。
func (s *Store) SaveJob(j *Job) error {
	return saveJob(s.db, j)
}

// SetMemo 只写用户备注，调度器手中的旧任务记录不会覆盖它。
func (s *Store) SetMemo(id int64, memo string) (*Job, error) {
	if _, err := s.db.Exec("UPDATE jobs SET memo=? WHERE id=?", memo, id); err != nil {
		return nil, err
	}
	return s.GetJob(id)
}

// SetNotifyDone 与收尾串行，由调度器验证任务仍在排队或运行。
func (s *Store) SetNotifyDone(id int64, on bool) (*Job, error) {
	if _, err := s.db.Exec("UPDATE jobs SET notify_done=? WHERE id=?", on, id); err != nil {
		return nil, err
	}
	return s.GetJob(id)
}

// RenameJob 只改任务名。SaveJob 不写名字，调度器手里的旧副本落库时不会把新名字改回去。
func (s *Store) RenameJob(id int64, name string) error {
	res, err := s.db.Exec("UPDATE jobs SET name=? WHERE id=?", name, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type jobWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

func saveJob(w jobWriter, j *Job) error {
	var exit any
	if j.ExitCode != nil {
		exit = *j.ExitCode
	}
	recipeJSON := "{}"
	if j.Recipe != nil {
		b, err := json.Marshal(j.Recipe)
		if err != nil {
			return err
		}
		recipeJSON = string(b)
	}
	profileJSON, err := json.Marshal(j.Profile)
	if err != nil {
		return err
	}
	res, err := w.Exec(`UPDATE jobs SET gpus_assigned=?, status=?, master_port=?, pid=?, pid_starttime=?,
		exit_code=?, fail_reason=?, cancel_requested=?, tracker_url=?, git_commit=?, git_branch=?, git_dirty=?,
		snapshot_ok=?, snapshot_note=?, started_at=?, ended_at=?, boot_id=?, notes=?, fail_kind=?, ext_seen=?,
		oom_base=?, traceback_offset=?, recipe_json=?, output_dir=?, profile_json=? WHERE id=?`,
		JoinInts(j.GPUsAssigned), j.Status, j.MasterPort, j.PID, int64(j.PIDStart),
		exit, j.FailReason, j.CancelRequested, j.TrackerURL, j.GitCommit, j.GitBranch, j.GitDirty,
		j.SnapshotOK, j.SnapshotNote, j.StartedAt, j.EndedAt, j.BootID, j.Notes, j.FailKind, j.ExtSeen,
		j.OOMBase, j.TracebackOffset, recipeJSON, j.OutputDir, string(profileJSON), j.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) GetJob(id int64) (*Job, error) {
	return scanJob(s.db.QueryRow("SELECT "+jobCols+" FROM jobs WHERE id=?", id))
}

// JobsByStatus 按 id 升序返回，排队任务的顺序就是提交顺序。
func (s *Store) JobsByStatus(status string) ([]*Job, error) {
	return s.queryJobs("SELECT "+jobCols+" FROM jobs WHERE status=? ORDER BY id", status)
}

// JobsByWorkdirs 返回工作目录等于 paths 中任一路径的任务。
func (s *Store) JobsByWorkdirs(paths []string) ([]*Job, error) {
	if len(paths) == 0 {
		return []*Job{}, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(paths)), ",")
	args := make([]any, len(paths))
	for i, path := range paths {
		args[i] = path
	}
	return s.queryJobs("SELECT "+jobCols+" FROM jobs WHERE workdir IN ("+marks+") ORDER BY id DESC", args...)
}

func (s *Store) JobsWithPendingRecipe() ([]*Job, error) {
	return s.queryJobs("SELECT " + jobCols + " FROM jobs WHERE COALESCE(json_extract(recipe_json,'$.pending'),0)=1 ORDER BY id")
}

type JobFilter struct {
	Statuses                           []string
	Workdir                            string
	Query                              string
	EndedAfter                         int64 // 只要这个时间（unix 秒）之后结束的任务；0 表示不限
	FinishedFromEvent, FinishedToEvent int64 // 包含两个端点，只匹配任务结束事件
	Starred                            bool
	// ByEnded 为 true 时只要已结束的任务，按结束时间从新到旧排；否则按 id 从新到旧。
	// 多台服务器的历史要按结束时间合并，所以这种排序配合 Before 游标翻页，不用页码。
	ByEnded bool
	// Before、BeforeID 是上一页最后一条的结束时间和 id，只在 ByEnded 时生效。
	// 结束时间只精确到秒，同一秒结束的任务再按 id 区分。BeforeID 为 0 时取这一秒之前的。
	Before   int64
	BeforeID int64
	Limit    int
	Offset   int
}

func (s *Store) ListJobs(f JobFilter) ([]*Job, int, error) {
	var where []string
	var args []any
	if f.Workdir != "" {
		where = append(where, "rtrim(workdir,'/')=rtrim(?,'/')")
		args = append(args, f.Workdir)
	}
	if f.Starred {
		where = append(where, "starred=1")
	}
	if len(f.Statuses) > 0 {
		where = append(where, "status IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Statuses)), ",")+")")
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.Query != "" {
		where = append(where, "(name LIKE ? OR cmd LIKE ? OR workdir LIKE ? OR memo LIKE ?)")
		q := "%" + f.Query + "%"
		args = append(args, q, q, q, q)
	}
	if f.EndedAfter > 0 {
		where = append(where, "ended_at >= ?")
		args = append(args, f.EndedAfter)
	}
	if f.FinishedFromEvent > 0 {
		where = append(where, "id IN (SELECT job_id FROM events WHERE kind=? AND id BETWEEN ? AND ?)")
		args = append(args, EventJobFinished, f.FinishedFromEvent, f.FinishedToEvent)
	}
	order := " ORDER BY id DESC"
	if f.ByEnded {
		where = append(where, "ended_at IS NOT NULL")
		order = " ORDER BY ended_at DESC, id DESC"
	}
	cond := func(w []string) string {
		if len(w) == 0 {
			return ""
		}
		return " WHERE " + strings.Join(w, " AND ")
	}
	// total 不算游标：它是符合筛选条件的总数，不随翻页变化
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM jobs"+cond(where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.ByEnded && f.Before > 0 {
		if f.BeforeID > 0 {
			where = append(where, "(ended_at < ? OR (ended_at = ? AND id < ?))")
			args = append(args, f.Before, f.Before, f.BeforeID)
		} else {
			where = append(where, "ended_at < ?")
			args = append(args, f.Before)
		}
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	jobs, err := s.queryJobs("SELECT "+jobCols+" FROM jobs"+cond(where)+order+" LIMIT ? OFFSET ?",
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

// ---------- 设备令牌 ----------

// DeviceToken 是发给桌面客户端的长期令牌。库里只存哈希，令牌本身只在创建时显示一次。
type DeviceToken struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	LastIP     string `json:"last_ip"`
}

func (s *Store) CreateDeviceToken(name, tokenHash string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO device_tokens (name, token_hash, created_at) VALUES (?,?,?)",
		truncate(name, 100), tokenHash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeviceTokenID 返回令牌哈希对应的 id；没有这个令牌（或已吊销）时返回 ErrNotFound。
func (s *Store) DeviceTokenID(tokenHash string) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM device_tokens WHERE token_hash=?", tokenHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func (s *Store) TouchDeviceToken(id int64, ip string) {
	_, _ = s.db.Exec("UPDATE device_tokens SET last_used_at=?, last_ip=? WHERE id=?", time.Now().Unix(), ip, id)
}

func (s *Store) ListDeviceTokens() ([]DeviceToken, error) {
	rows, err := s.db.Query("SELECT id, name, created_at, last_used_at, last_ip FROM device_tokens ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceToken{}
	for rows.Next() {
		var (
			t    DeviceToken
			used sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &used, &t.LastIP); err != nil {
			return nil, err
		}
		if used.Valid {
			t.LastUsedAt = &used.Int64
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeDeviceToken 直接删除令牌，返回被删令牌的名字。
func (s *Store) RevokeDeviceToken(id int64) (string, error) {
	var name string
	err := s.db.QueryRow("DELETE FROM device_tokens WHERE id=? RETURNING name", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
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
// 计数加一和锁定在同一条语句里完成，并发的失败不会互相覆盖；已有的锁定只会延长，不会被清掉。
func (s *Store) RecordFailure(ip string, max int, lockout time.Duration) (bool, error) {
	until := time.Now().Add(lockout).Unix()
	var lockedUntil int64
	err := s.db.QueryRow(`INSERT INTO login_failures (ip, count, locked_until)
		VALUES (?1, CASE WHEN 1 >= ?2 THEN 0 ELSE 1 END, CASE WHEN 1 >= ?2 THEN ?3 ELSE 0 END)
		ON CONFLICT(ip) DO UPDATE SET
			count = CASE WHEN login_failures.count + 1 >= ?2 THEN 0 ELSE login_failures.count + 1 END,
			locked_until = CASE WHEN login_failures.count + 1 >= ?2 THEN ?3 ELSE login_failures.locked_until END
		RETURNING locked_until`, ip, max, until).Scan(&lockedUntil)
	return err == nil && lockedUntil == until, err
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
