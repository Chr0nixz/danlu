package main

// 任务命令（FEATURES 5.1）：submit、jobs、show、tail、wait、cancel、rerun、gpus、api。
// 都经 apiClient 调用本机正在运行的 danlu（数据目录里的 danlu.sock），不直接读写数据库：
// 所有状态变化仍然只经过 scheduler.Manager。约定（退出码、--json、stdout 只放结果）见 exit.go。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"danlu/internal/logs"
	"danlu/internal/scheduler"
	"danlu/internal/shellquote"
	"danlu/internal/store"

	"golang.org/x/term"
)

// jobInfo 是 /api 任务对象里命令行要用的部分：store.Job 加上接口附带的字段。
type jobInfo struct {
	store.Job
	LastLine   string `json:"last_line"`
	ErrorLine  string `json:"error_line"`
	QueuePos   int    `json:"queue_pos"`
	WaitReason string `json:"wait_reason"`
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// parseInterleaved 允许参数和位置参数交错（danlu wait 42 --timeout 6h）；-- 之后全部是位置参数。
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := parseFlags(fs, args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if i := len(args) - len(rest); i > 0 && args[i-1] == "--" {
			return append(pos, rest...), nil
		}
		pos, args = append(pos, rest[0]), rest[1:]
	}
}

func parseJobIDs(args []string) ([]int64, error) {
	if len(args) == 0 {
		return nil, usageErr("缺少任务编号")
	}
	var ids []int64
	for _, a := range args {
		id, err := strconv.ParseInt(strings.TrimPrefix(a, "#"), 10, 64)
		if err != nil || id <= 0 {
			return nil, usageErr("任务编号 %q 无效", a)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func oneJobID(args []string) (int64, error) {
	if len(args) != 1 {
		return 0, usageErr("需要一个任务编号")
	}
	ids, err := parseJobIDs(args)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// printRawJSON 把服务端的 JSON 原样（缩进后）写到 stdout，字段与 /api 完全相同。
func printRawJSON(b []byte) error {
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(b), "", "  "); err != nil {
		return fmt.Errorf("服务端响应不是 JSON: %w", err)
	}
	out.WriteByte('\n')
	_, err := os.Stdout.Write(out.Bytes())
	return err
}

// getJSON 发 GET，返回原始响应体；错误响应转成 cliError。
func (c *apiClient) getJSON(ctx context.Context, path string) ([]byte, error) {
	r, err := c.raw(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if r.status >= 400 {
		return nil, apiError(r)
	}
	return r.body, nil
}

func (c *apiClient) getJob(ctx context.Context, id int64) ([]byte, *jobInfo, error) {
	b, err := c.getJSON(ctx, fmt.Sprintf("/api/jobs/%d", id))
	if err != nil {
		return nil, nil, err
	}
	var j jobInfo
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, nil, fmt.Errorf("服务端响应不是任务对象: %w", err)
	}
	return b, &j, nil
}

// ---------- submit / rerun ----------

// prefs 读面板偏好（GET /api/settings 的 prefs），面板上改的默认值对命令行同样生效。
type newJobPrefs struct {
	GPUs   int  `json:"new_job_gpus"`
	Bashrc bool `json:"new_job_bashrc"`
	Notify bool `json:"new_job_notify_done"`
}

func (c *apiClient) prefs(ctx context.Context) (newJobPrefs, error) {
	p := newJobPrefs{GPUs: 1}
	var st struct {
		Prefs *newJobPrefs `json:"prefs"`
	}
	st.Prefs = &p
	_, err := c.do(ctx, http.MethodGet, "/api/settings", nil, &st)
	return p, err
}

// boolPair 处理 --x / --no-x：都没写时 ok 为 false，用默认值。
func boolPair(set map[string]bool, on, off string) (val, ok bool, err error) {
	switch {
	case set[on] && set[off]:
		return false, false, usageErr("--%s 和 --%s 只能写一个", on, off)
	case set[on]:
		return true, true, nil
	case set[off]:
		return false, true, nil
	}
	return false, false, nil
}

func visited(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func cmdSubmit(args []string) error {
	fs := newFlags("submit", "danlu submit [选项] -- <命令…>  或  danlu submit [选项] --cmd-file <文件|->\n"+
		"  -- 后面只有一个参数时原样作为 shell 脚本；有多个参数时逐个加引号再拼接，重跑时得到同一个 argv。\n"+
		"  任务不继承当前 shell 的环境变量：用 --var 传，和面板提交一样。")
	workdir := fs.String("C", "", "工作目录（默认当前目录）")
	gpus := fs.Int("g", 0, "GPU 张数，0 是纯 CPU 任务（不写按面板偏好）")
	pin := fs.String("pin", "", "指定卡号，如 0,2（与 -g 二选一）")
	conda := fs.String("e", "", "conda 环境名（不写就不激活）")
	var vars multiFlag
	fs.Var(&vars, "var", "环境变量 K=V，可重复")
	fs.Bool("bashrc", false, "继承 ~/.bashrc（不写按面板偏好）")
	fs.Bool("no-bashrc", false, "不继承 ~/.bashrc")
	name := fs.String("n", "", "任务名（默认按目录名和时间生成）")
	memo := fs.String("memo", "", "备注")
	fs.Bool("notify", false, "结束时手机推送（不写按面板偏好）")
	fs.Bool("no-notify", false, "结束时不推送")
	key := fs.String("key", "", "提交幂等键：同一个 key 只建一个任务，连接断开后可以放心重试")
	cmdFile := fs.String("cmd-file", "", "从文件读命令（多行脚本），- 表示标准输入")
	asJSON := fs.Bool("json", false, "以 JSON 输出任务对象")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	set := visited(fs)
	var cmd string
	switch {
	case *cmdFile != "" && fs.NArg() > 0:
		return usageErr("--cmd-file 和 -- 后面的命令只能二选一")
	case *cmdFile != "":
		b, err := readInput(*cmdFile, maxCmdBytes, "命令脚本")
		if err != nil {
			return err
		}
		cmd = string(b)
	case fs.NArg() == 1:
		cmd = fs.Arg(0)
	case fs.NArg() > 1:
		cmd = shellquote.Join(fs.Args())
	default:
		return usageErr("缺少命令：danlu submit [选项] -- <命令…>")
	}
	if strings.TrimSpace(cmd) == "" {
		return usageErr("命令是空的")
	}
	if set["g"] && set["pin"] {
		return usageErr("-g 和 --pin 只能写一个")
	}
	var pinned []int
	if set["pin"] {
		for _, s := range strings.Split(*pin, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < 0 {
				return usageErr("--pin 的卡号 %q 无效，例如 --pin 0,2", s)
			}
			pinned = append(pinned, n)
		}
	}
	env := map[string]string{}
	for _, v := range vars {
		k, val, ok := strings.Cut(v, "=")
		if !ok || k == "" {
			return usageErr("--var 要写成 K=V：%q", v)
		}
		env[k] = val
	}
	bashrc, bashrcSet, err := boolPair(set, "bashrc", "no-bashrc")
	if err != nil {
		return err
	}
	notify, notifySet, err := boolPair(set, "notify", "no-notify")
	if err != nil {
		return err
	}
	dir := *workdir
	if dir == "" {
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	} else if dir, err = filepath.Abs(dir); err != nil {
		return err
	}
	c := newAPIClient()
	ctx := context.Background()
	n := *gpus
	if !set["g"] && !set["pin"] || !bashrcSet || !notifySet {
		p, err := c.prefs(ctx)
		if err != nil {
			return err
		}
		if !set["g"] && !set["pin"] {
			n = p.GPUs
		}
		if !bashrcSet {
			bashrc = p.Bashrc
		}
		if !notifySet {
			notify = p.Notify
		}
	}
	req := scheduler.SubmitReq{Name: *name, Memo: *memo, NotifyDone: notify, Workdir: filepath.Clean(dir),
		CondaEnv: *conda, Cmd: cmd, Env: env, InheritBashrc: bashrc, GPUs: n, PinnedGPUs: pinned, SubmitKey: *key}
	return postJob(ctx, c, req, *asJSON, "")
}

// maxCmdBytes：命令脚本的上限。提交请求整体不能超过 1 MiB（服务端的请求体上限），脚本占一半足够。
const maxCmdBytes = 512 << 10

// readInput 读文件或标准输入（"-"），超过 limit 直接报错：截断的脚本照样能跑，后果比报错严重。
func readInput(name string, limit int64, what string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, usageErr("%s超过 %d KiB", what, limit>>10)
	}
	return b, nil
}

// postJob 提交并打印结果：--json 原样输出任务对象；文字给编号、排队位置和面板路径（不带安全入口）。
func postJob(ctx context.Context, c *apiClient, req scheduler.SubmitReq, asJSON bool, note string) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r, err := c.raw(ctx, http.MethodPost, "/api/jobs", body)
	if err != nil {
		return err
	}
	if r.status >= 400 {
		return apiError(r)
	}
	if asJSON {
		return printRawJSON(r.body)
	}
	var j jobInfo
	if err := json.Unmarshal(r.body, &j); err != nil {
		return fmt.Errorf("服务端响应不是任务对象: %w", err)
	}
	switch {
	case r.status == http.StatusOK:
		fmt.Printf("已存在 #%d（同一个 --key），状态 %s\n", j.ID, j.Status)
	case j.QueuePos > 0:
		fmt.Printf("已提交 #%d（排队第 %d 位）%s\n", j.ID, j.QueuePos, note)
	default:
		fmt.Printf("已提交 #%d（%s）%s\n", j.ID, j.Status, note)
	}
	fmt.Printf("面板路径 /jobs/%d\n", j.ID)
	return nil
}

var rerunSuffix = regexp.MustCompile(`^(.*)-r(\d+)$`)

// rerunName 与面板的重跑命名相同（web/src/pages/NewJob.tsx）：x → x-r2，x-r2 → x-r3。
func rerunName(name string) string {
	if m := rerunSuffix.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[2])
		return fmt.Sprintf("%s-r%d", m[1], n+1)
	}
	return name + "-r2"
}

// sameCodeFailKinds 与 @danlu/core 的同名列表一致：这些失败和代码无关，重跑默认用原任务的快照。
var sameCodeFailKinds = []string{"reboot", "killed", "signal", "nccl"}

func cmdRerun(args []string) error {
	fs := newFlags("rerun", "danlu rerun <id> [--snapshot | --current-code] [选项]\n"+
		"  复制原任务的工作目录、命令、conda、环境变量和 GPU 要求。都不写时：被重启、外部终止这类与代码无关的失败用快照，否则用工作目录现在的代码。")
	fs.Bool("snapshot", false, "用原任务的代码快照")
	fs.Bool("current-code", false, "用工作目录现在的代码")
	name := fs.String("n", "", "任务名（默认在原名后加 -r2、-r3…）")
	memo := fs.String("memo", "", "备注")
	fs.Bool("notify", false, "结束时手机推送（不写按面板偏好）")
	fs.Bool("no-notify", false, "结束时不推送")
	key := fs.String("key", "", "提交幂等键")
	asJSON := fs.Bool("json", false, "以 JSON 输出新任务对象")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	id, err := oneJobID(pos)
	if err != nil {
		return err
	}
	set := visited(fs)
	useSnap, snapSet, err := boolPair(set, "snapshot", "current-code")
	if err != nil {
		return err
	}
	notify, notifySet, err := boolPair(set, "notify", "no-notify")
	if err != nil {
		return err
	}
	c := newAPIClient()
	ctx := context.Background()
	_, j, err := c.getJob(ctx, id)
	if err != nil {
		return err
	}
	if !snapSet {
		canRestore := j.SnapshotOK && j.PrunedAt == nil && (j.PID > 0 || j.EndedAt != nil)
		useSnap = canRestore && slices.Contains(sameCodeFailKinds, j.FailKind)
	}
	if !notifySet {
		p, err := c.prefs(ctx)
		if err != nil {
			return err
		}
		notify = p.Notify
	}
	req := scheduler.SubmitReq{Name: *name, Memo: *memo, NotifyDone: notify, Workdir: j.Workdir, CondaEnv: j.CondaEnv,
		Cmd: j.Cmd, Env: j.Env, InheritBashrc: j.InheritBashrc, GPUs: j.GPUsReq, PinnedGPUs: j.GPUsPinned,
		RerunOf: &id, SubmitKey: *key}
	if req.Name == "" {
		req.Name = rerunName(j.Name)
	}
	note := "，用工作目录现在的代码"
	if useSnap {
		req.CodeFrom, note = &id, fmt.Sprintf("，用 #%d 的代码快照", id)
	}
	return postJob(ctx, c, req, *asJSON, note)
}

// ---------- jobs / show ----------

func cmdJobs(args []string) error {
	fs := newFlags("jobs", "danlu jobs [--status s1,s2] [-q 文本] [--workdir 目录] [--limit N] [--json]")
	status := fs.String("status", "", "按状态筛选：queued,running,succeeded,failed,cancelled")
	query := fs.String("q", "", "按名字、命令、备注搜索")
	workdir := fs.String("workdir", "", "只看这个工作目录的任务（. 表示当前目录）")
	limit := fs.Int("limit", 20, "最多显示多少个（1–200）")
	asJSON := fs.Bool("json", false, "以 JSON 输出（与 GET /api/jobs 相同）")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageErr("jobs 不接受位置参数：%s", strings.Join(pos, " "))
	}
	if *limit < 1 || *limit > 200 {
		return usageErr("--limit 要在 1 到 200 之间")
	}
	q := url.Values{"size": {strconv.Itoa(*limit)}}
	if *status != "" {
		q.Set("status", *status)
	}
	if *query != "" {
		q.Set("q", *query)
	}
	if *workdir != "" {
		dir, err := filepath.Abs(*workdir)
		if err != nil {
			return err
		}
		q.Set("workdir", dir)
	}
	b, err := newAPIClient().getJSON(context.Background(), "/api/jobs?"+q.Encode())
	if err != nil {
		return err
	}
	if *asJSON {
		return printRawJSON(b)
	}
	var list struct {
		Jobs  []jobInfo `json:"jobs"`
		Total int       `json:"total"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("服务端响应不是任务列表: %w", err)
	}
	if len(list.Jobs) == 0 {
		fmt.Println("没有任务")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	now := time.Now()
	for _, j := range list.Jobs {
		fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\n", j.ID, j.Status, gpuText(&j), durText(&j, now),
			clip(j.Name, 40), clip(jobDetail(&j), 80))
	}
	tw.Flush()
	if list.Total > len(list.Jobs) {
		fmt.Printf("共 %d 个，显示最近 %d 个（--limit 调整）\n", list.Total, len(list.Jobs))
	}
	return nil
}

func cmdShow(args []string) error {
	fs := newFlags("show", "danlu show <id> [--json]")
	asJSON := fs.Bool("json", false, "以 JSON 输出（与 GET /api/jobs/{id} 相同）")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	id, err := oneJobID(pos)
	if err != nil {
		return err
	}
	b, j, err := newAPIClient().getJob(context.Background(), id)
	if err != nil {
		return err
	}
	if *asJSON {
		return printRawJSON(b)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	fmt.Fprintf(tw, "#%d\t%s\n", j.ID, j.Name)
	state := j.Status
	if j.ExitCode != nil {
		state += fmt.Sprintf("（退出码 %d）", *j.ExitCode)
	}
	if j.FailReason != "" {
		state += "：" + j.FailReason
	}
	if j.CancelRequested && !j.Finished() {
		state += "（正在取消）"
	}
	row("状态", state)
	if j.Status == store.StatusQueued {
		row("排队", fmt.Sprintf("第 %d 位 %s", j.QueuePos, j.WaitReason))
	}
	row("工作目录", j.Workdir)
	row("命令", j.Cmd)
	row("conda", j.CondaEnv)
	row("GPU", gpuText(j))
	keys := make([]string, 0, len(j.Env))
	for k := range j.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		row("环境变量", k+"="+j.Env[k])
	}
	if j.InheritBashrc {
		row(".bashrc", "继承")
	}
	if j.GitCommit != "" {
		code := "git " + j.GitCommit
		if j.GitBranch != "" {
			code += " " + j.GitBranch
		}
		if j.GitDirty {
			code += "（有未提交的改动）"
		}
		row("代码", code)
	}
	switch {
	case j.PrunedAt != nil:
		row("快照", "已按保留策略清理")
	case j.SnapshotOK:
		row("快照", "可用")
	case j.SnapshotNote != "":
		row("快照", j.SnapshotNote)
	}
	if j.CodeFrom != nil {
		row("代码来自", fmt.Sprintf("#%d 的快照", *j.CodeFrom))
	}
	if j.RerunOf != nil {
		row("重跑自", fmt.Sprintf("#%d", *j.RerunOf))
	}
	row("提交", fmtTime(j.CreatedAt))
	if j.StartedAt != nil {
		row("开始", fmtTime(*j.StartedAt))
	}
	if j.EndedAt != nil {
		row("结束", fmtTime(*j.EndedAt))
	}
	row("用时", durText(j, time.Now()))
	row("输出目录", j.OutputDir)
	row("跟踪", j.TrackerURL)
	row("备注", j.Memo)
	row("幂等键", j.SubmitKey)
	row("最后一行", j.LastLine)
	row("报错行", j.ErrorLine)
	row("面板路径", fmt.Sprintf("/jobs/%d", j.ID))
	return tw.Flush()
}

func gpuText(j *jobInfo) string {
	switch {
	case len(j.GPUsAssigned) > 0:
		return "GPU " + joinInts(j.GPUsAssigned)
	case len(j.GPUsPinned) > 0:
		return "指定 " + joinInts(j.GPUsPinned)
	case j.GPUsReq == 0:
		return "CPU"
	}
	return fmt.Sprintf("%d 张", j.GPUsReq)
}

func jobDetail(j *jobInfo) string {
	switch j.Status {
	case store.StatusQueued:
		return j.WaitReason
	case store.StatusFailed:
		return j.FailReason
	case store.StatusCancelled:
		return ""
	}
	return j.LastLine
}

func durText(j *jobInfo, now time.Time) string {
	switch {
	case j.Status == store.StatusQueued:
		return "等 " + fmtDur(now.Sub(time.Unix(j.CreatedAt, 0)))
	case j.StartedAt == nil:
		return "-"
	case j.EndedAt != nil:
		return fmtDur(time.Unix(*j.EndedAt, 0).Sub(time.Unix(*j.StartedAt, 0)))
	}
	return fmtDur(now.Sub(time.Unix(*j.StartedAt, 0)))
}

func fmtDur(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func fmtTime(unix int64) string { return time.Unix(unix, 0).Format("2006-01-02 15:04:05") }

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ---------- wait ----------

// outcome 把任务的结束状态换成 wait、tail -f 的退出码；没结束返回 -1。
func outcome(status string) int {
	switch status {
	case store.StatusSucceeded:
		return 0
	case store.StatusFailed:
		return exitJobFailed
	case store.StatusCancelled:
		return exitJobCancelled
	}
	return -1
}

func cmdWait(args []string) error {
	fs := newFlags("wait", "danlu wait <id>… [--timeout 时长] [--json]\n"+
		"  等到这些任务都结束。退出码：全部成功 0，有失败 10，没有失败但有取消 11，超时 124，任务不存在 4。")
	timeout := fs.Duration("timeout", 0, "最多等多久，例如 6h（默认一直等）")
	asJSON := fs.Bool("json", false, `以 JSON 输出 {"jobs":[任务对象…]}`)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	ids, err := parseJobIDs(pos)
	if err != nil {
		return err
	}
	if *timeout < 0 {
		return usageErr("--timeout 不能是负数")
	}
	c := newAPIClient()
	// --timeout 是整个等待的期限，服务重启期间的重试也算在里面，到点就按超时退出
	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	raws := make(map[int64][]byte, len(ids))
	jobs := make(map[int64]*jobInfo, len(ids))
	timedOut := false
poll:
	for {
		pending := 0
		for _, id := range ids {
			if j := jobs[id]; j != nil && j.Finished() {
				continue
			}
			err := withRestartRetry(ctx, func() error {
				b, j, err := c.getJob(ctx, id)
				if err == nil {
					raws[id], jobs[id] = b, j
				}
				return err
			})
			if errors.Is(err, context.DeadlineExceeded) {
				timedOut = true
				break poll
			}
			if err != nil {
				return err
			}
			if !jobs[id].Finished() {
				pending++
			}
		}
		if pending == 0 {
			break
		}
		select {
		case <-ctx.Done():
			timedOut = true
			break poll
		case <-time.After(2 * time.Second):
		}
	}
	code := 0
	for _, id := range ids {
		if jobs[id] == nil {
			continue // 到期前一次都没读到（服务一直连不上）
		}
		switch outcome(jobs[id].Status) {
		case exitJobFailed:
			code = exitJobFailed
		case exitJobCancelled:
			if code == 0 {
				code = exitJobCancelled
			}
		}
	}
	if timedOut {
		code = exitTimeout
	}
	if *asJSON {
		list := make([]json.RawMessage, len(ids))
		for i, id := range ids {
			list[i] = raws[id]
			if list[i] == nil {
				list[i] = json.RawMessage("null")
			}
		}
		b, err := json.Marshal(map[string]any{"jobs": list})
		if err != nil {
			return err
		}
		if err := printRawJSON(b); err != nil {
			return err
		}
	} else {
		now := time.Now()
		for _, id := range ids {
			j := jobs[id]
			if j == nil {
				fmt.Printf("#%d 状态未知：等待期间一直连不上服务\n", id)
				continue
			}
			line := fmt.Sprintf("#%d %s", j.ID, j.Status)
			if j.Finished() {
				line += "  用时 " + durText(j, now)
			} else {
				line += "  还没结束"
			}
			line += "  " + j.Name
			if j.FailReason != "" {
				line += "：" + j.FailReason
			}
			fmt.Println(line)
		}
	}
	if code != 0 {
		return silentExit(code)
	}
	return nil
}

// ---------- cancel ----------

func cmdCancel(args []string) error {
	fs := newFlags("cancel", "danlu cancel <id>… [--json]\n  排队中的直接取消；运行中的先发 SIGTERM，宽限期后强制结束。")
	asJSON := fs.Bool("json", false, `以 JSON 输出 {"jobs":[任务对象…],"errors":[…]}`)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	ids, err := parseJobIDs(pos)
	if err != nil {
		return err
	}
	c := newAPIClient()
	ctx := context.Background()
	type failure struct {
		ID    int64  `json:"id"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	done := []json.RawMessage{}
	failed := []failure{}
	code := 0
	for _, id := range ids {
		var raw json.RawMessage
		_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/jobs/%d/cancel", id), nil, &raw)
		var ce *cliError
		if errors.As(err, &ce) && ce.exit == exitUnreachable {
			return err // 连不上服务，后面的也不用试了
		}
		if err != nil {
			failed = append(failed, failure{ID: id, Error: err.Error()})
			exit := exitErr
			if errors.As(err, &ce) {
				failed[len(failed)-1].Code, exit = ce.code, ce.exit
			}
			if code == 0 || code == exitNotFound {
				code = exit
			}
			if !*asJSON {
				fmt.Fprintf(os.Stderr, "错误: #%d：%v\n", id, err)
			}
			continue
		}
		done = append(done, raw)
		if !*asJSON {
			var j jobInfo
			_ = json.Unmarshal(raw, &j)
			if j.Status == store.StatusCancelled {
				fmt.Printf("已取消 #%d\n", id)
			} else {
				fmt.Printf("#%d 正在停止（已发 SIGTERM，宽限期后强制结束）\n", id)
			}
		}
	}
	if *asJSON {
		b, err := json.Marshal(map[string]any{"jobs": done, "errors": failed})
		if err != nil {
			return err
		}
		if err := printRawJSON(b); err != nil {
			return err
		}
	}
	if code != 0 {
		return silentExit(code)
	}
	return nil
}

// ---------- tail ----------

func cmdTail(args []string) error {
	fs := newFlags("tail", "danlu tail <id> [-n 行数] [-f] [--raw]\n"+
		"  默认把 tqdm 的 \\r 进度折叠成最后一次的状态；输出不是终端时去掉颜色。-f 跟到任务结束，退出码同 danlu wait。")
	n := fs.Int("n", 50, "显示最后多少行")
	follow := fs.Bool("f", false, "跟随输出直到任务结束")
	raw := fs.Bool("raw", false, "原样输出日志字节（不折叠进度条、不去颜色）")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	id, err := oneJobID(pos)
	if err != nil {
		return err
	}
	if *n < 0 {
		return usageErr("-n 不能是负数")
	}
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	c := newAPIClient()
	ctx := context.Background()
	next, err := printTail(ctx, c, id, *n, *raw, tty, *follow)
	if err != nil {
		return err
	}
	if !*follow {
		return nil
	}
	status, err := followLog(ctx, c, id, next, *raw || tty)
	if err != nil {
		return err
	}
	if code := outcome(status); code > 0 {
		return silentExit(code)
	}
	return nil
}

const maxTailBytes = 4 << 20 // 与 /api/jobs/{id}/log?tail= 的上限一致

// printTail 打印最后 n 行，返回 tail -f 续读的偏移。follow 时最后一行还没写完（没有换行）就先不打印，
// 从它的开头续读，免得同一行被拆成两行。
func printTail(ctx context.Context, c *apiClient, id int64, n int, raw, tty, follow bool) (int64, error) {
	var res struct {
		Data  string `json:"data"`
		Start int64  `json:"start"`
		Next  int64  `json:"next"`
	}
	var lines []string
	var resume int64
	for want := int64(256 << 10); ; want = min(want*4, maxTailBytes) {
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/jobs/%d/log?tail=%d", id, want), nil, &res); err != nil {
			return 0, err
		}
		text := res.Data
		resume = res.Next
		if res.Start > 0 {
			// 取到的第一行多半只有后半截
			if i := strings.IndexByte(text, '\n'); i >= 0 {
				text = text[i+1:]
			}
		}
		if follow {
			// 没写完的最后一行（没有换行的部分）留给续读，从它的开头接着读
			cut := strings.LastIndexByte(text, '\n') + 1
			resume -= int64(len(text) - cut)
			text = text[:cut]
		}
		if !raw {
			text = logs.CollapseCR(text)
		}
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		if text == "" {
			lines = nil
		}
		if len(lines) >= n || res.Start == 0 || want >= maxTailBytes {
			break
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) > 0 {
		out := strings.Join(lines, "\n") + "\n"
		if !raw && !tty {
			out = logs.StripANSI(out)
		}
		if _, err := io.WriteString(os.Stdout, out); err != nil {
			return 0, err
		}
	}
	return resume, nil
}

// followLog 从 off 开始读日志流，直到任务结束，返回结束状态。passthrough 为 true 时原样输出
// （终端自己处理 \r 和颜色），否则按行折叠进度条、去掉颜色。服务重启的间隙会重连。
func followLog(ctx context.Context, c *apiClient, id int64, off int64, passthrough bool) (string, error) {
	var pending string // 还没换行的半行，等它完整了再折叠
	emit := func(t string) error {
		if passthrough {
			_, err := io.WriteString(os.Stdout, t)
			return err
		}
		pending += t
		i := strings.LastIndexByte(pending, '\n')
		if i < 0 {
			return nil
		}
		out := logs.StripANSI(logs.CollapseCR(pending[:i+1]))
		pending = pending[i+1:]
		_, err := io.WriteString(os.Stdout, out)
		return err
	}
	flush := func() {
		if !passthrough && pending != "" {
			_, _ = io.WriteString(os.Stdout, logs.StripANSI(logs.CollapseCR(pending))+"\n")
			pending = ""
		}
	}
	for {
		var status string
		err := withRestartRetry(ctx, func() error {
			var err error
			status, err = streamLog(ctx, c, id, &off, emit)
			return err
		})
		if err != nil {
			flush()
			return "", err
		}
		if status != "" {
			flush()
			return status, nil
		}
		// 流断了但没收到 end（服务在重启），稍等后从当前偏移重连
		time.Sleep(time.Second)
	}
}

// streamLog 读一次 SSE：data 事件是 {"o","n","t"}，按字节偏移去重；end 事件带结束状态。
// 正常读到流结束但没有 end 时返回空状态，由调用方重连。
func streamLog(ctx context.Context, c *apiClient, id int64, off *int64, emit func(string) error) (string, error) {
	resp, err := c.open(ctx, http.MethodGet, fmt.Sprintf("/api/jobs/%d/log/stream?offset=%d", id, *off), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", apiError(rawResponse{status: resp.StatusCode, body: b})
	}
	rd := bufio.NewReaderSize(resp.Body, 256<<10)
	event, data := "", ""
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", nil // 连接断了：调用方重连
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimPrefix(line[len("data:"):], " ")
		case line == "" && data != "":
			if event == "end" {
				var end struct {
					Status string `json:"status"`
				}
				if json.Unmarshal([]byte(data), &end) == nil && end.Status != "" {
					return end.Status, nil
				}
			} else {
				var chunk struct {
					O int64  `json:"o"`
					N int64  `json:"n"`
					T string `json:"t"`
				}
				if json.Unmarshal([]byte(data), &chunk) == nil && chunk.N > *off {
					t := chunk.T
					if skip := *off - chunk.O; skip > 0 && skip <= int64(len(t)) {
						t = t[skip:]
					}
					*off = chunk.N
					if err := emit(t); err != nil {
						return "", err
					}
				}
			}
			event, data = "", ""
		}
	}
}

// ---------- gpus ----------

func cmdGpus(args []string) error {
	fs := newFlags("gpus", "danlu gpus [--json]\n  每张卡被谁占着（任务、外部进程、预约），以及队首在等什么。")
	asJSON := fs.Bool("json", false, `以 JSON 输出 {"gpus":…,"queue":…,"queued":N,"queue_head":任务对象或 null}`)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usageErr("gpus 不接受位置参数")
	}
	c := newAPIClient()
	ctx := context.Background()
	gpusRaw, err := c.getJSON(ctx, "/api/gpus")
	if err != nil {
		return err
	}
	queueRaw, err := c.getJSON(ctx, "/api/queue")
	if err != nil {
		return err
	}
	// 只要队首：按队列顺序取第一条，排队任务再多也取得到；total 是排队总数
	queuedRaw, err := c.getJSON(ctx, "/api/jobs?status=queued&order=asc&size=1")
	if err != nil {
		return err
	}
	var queued struct {
		Jobs  []json.RawMessage `json:"jobs"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(queuedRaw, &queued); err != nil {
		return fmt.Errorf("服务端响应不是任务列表: %w", err)
	}
	var head *jobInfo
	var headRaw json.RawMessage
	for _, r := range queued.Jobs {
		var j jobInfo
		if json.Unmarshal(r, &j) == nil && j.QueuePos == 1 {
			head, headRaw = &j, r
		}
	}
	if *asJSON {
		b, err := json.Marshal(map[string]any{"gpus": json.RawMessage(gpusRaw), "queue": json.RawMessage(queueRaw),
			"queued": queued.Total, "queue_head": headRaw})
		if err != nil {
			return err
		}
		return printRawJSON(b)
	}
	var g struct {
		GPUs []struct {
			Index        int    `json:"index"`
			Name         string `json:"name"`
			Util         int    `json:"util"`
			MemUsedMB    int    `json:"mem_used_mb"`
			MemTotalMB   int    `json:"mem_total_mb"`
			UnknownMemMB int    `json:"unknown_mem_mb"`
			Job          *struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"job"`
			External []struct {
				PID       int    `json:"pid"`
				UsedMemMB int    `json:"used_mem_mb"`
				User      string `json:"user"`
			} `json:"external"`
			Reservation *struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
				EndsAt *int64 `json:"ends_at"`
			} `json:"reservation"`
		} `json:"gpus"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(gpusRaw, &g); err != nil {
		return fmt.Errorf("服务端响应不是 GPU 列表: %w", err)
	}
	var q scheduler.QueueState
	_ = json.Unmarshal(queueRaw, &q)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, card := range g.GPUs {
		var who []string
		if card.Job != nil {
			who = append(who, fmt.Sprintf("#%d %s", card.Job.ID, clip(card.Job.Name, 30)))
		}
		if r := card.Reservation; r != nil {
			s := fmt.Sprintf("预约 #%d（%s", r.ID, r.Status)
			if r.EndsAt != nil {
				s += "，到 " + time.Unix(*r.EndsAt, 0).Format("01-02 15:04")
			}
			who = append(who, s+"）")
		}
		for _, p := range card.External {
			who = append(who, fmt.Sprintf("外部进程 pid %d %s %.1f GiB", p.PID, p.User, float64(p.UsedMemMB)/1024))
		}
		if len(who) == 0 && card.UnknownMemMB > 0 {
			who = append(who, fmt.Sprintf("来源不明的显存占用 %.1f GiB", float64(card.UnknownMemMB)/1024))
		}
		if len(who) == 0 {
			who = append(who, "空闲")
		}
		fmt.Fprintf(tw, "GPU %d\t%s\t%d%%\t%.1f/%.1f GiB\t%s\n", card.Index, card.Name, card.Util,
			float64(card.MemUsedMB)/1024, float64(card.MemTotalMB)/1024, strings.Join(who, "；"))
	}
	tw.Flush()
	if g.Error != "" {
		fmt.Println("GPU 数据暂不可信，不会启动需要 GPU 的任务：" + g.Error)
	}
	if q.Paused {
		fmt.Println("队列已暂停：" + q.Reason)
	}
	line := fmt.Sprintf("排队 %d 个", queued.Total)
	if head != nil {
		line += fmt.Sprintf("；队首 #%d（%s）", head.ID, gpuText(head))
		if head.WaitReason != "" {
			line += "：" + head.WaitReason
		}
	}
	fmt.Println(line)
	return nil
}

// ---------- api ----------

func cmdAPI(args []string) error {
	fs := newFlags("api", "danlu api <方法> <路径> [--data JSON | --data @文件 | --data -]\n"+
		"  直接调用 /api 接口，原样打印响应体。2xx 退出码 0，任务不存在 4，其他错误 1。不支持日志流和终端这类长连接。")
	data := fs.String("data", "", "请求体：JSON 文本，@文件，或 - 表示标准输入（写操作不填时发 {}）")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErr("用法: danlu api <方法> <路径>，例如 danlu api GET /api/gpus")
	}
	method, path := strings.ToUpper(pos[0]), pos[1]
	if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, method) {
		return usageErr("不支持的方法 %q", pos[0])
	}
	if !strings.HasPrefix(path, "/api/") {
		return usageErr("路径必须以 /api/ 开头")
	}
	p := path
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if strings.HasSuffix(p, "/log/stream") || strings.HasSuffix(p, "/ws") {
		return usageErr("danlu api 不支持长连接接口；日志用 danlu tail -f")
	}
	var body []byte
	switch {
	case *data == "-":
		if body, err = readInput("-", 1<<20, "请求体"); err != nil {
			return err
		}
	case strings.HasPrefix(*data, "@"):
		if body, err = readInput((*data)[1:], 1<<20, "请求体"); err != nil {
			return err
		}
	case *data != "":
		body = []byte(*data)
	case method != "GET":
		body = []byte("{}")
	}
	if method == "GET" && body != nil {
		return usageErr("GET 请求不能带 --data")
	}
	// 成功的响应边读边写，不设大小上限（比如 log/download）；错误响应很小，读完再按码定退出码
	resp, err := newAPIClient().open(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 400 {
		_, err := io.Copy(os.Stdout, resp.Body)
		return err
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	os.Stdout.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	code := exitErr
	var ce *cliError
	if errors.As(apiError(rawResponse{status: resp.StatusCode, body: b}), &ce) {
		code = ce.exit
	}
	return silentExit(code)
}
