package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"danlu/internal/config"
	"danlu/internal/retention"
	"danlu/internal/store"
)

func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "只预览，不删除任务文件")
	older := fs.String("older-than", "", "清理结束超过这个时长的任务，例如 72h；0s 表示所有已结束任务")
	jsonOut := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("prune 不接受位置参数")
	}
	p := paths()
	c, err := config.Load(p)
	if err != nil {
		return err
	}
	output, err := config.ResolvePath(c.Runtime.OutputRoot)
	if err != nil {
		return err
	}
	opt := retention.Options{DryRun: *dryRun, KeepDays: c.Retention.KeepDays, MaxBytes: c.Retention.MaxTotalGB << 30}
	if *older != "" {
		d, err := time.ParseDuration(*older)
		if err != nil || d < 0 {
			return fmt.Errorf("--older-than 必须是非负时长，例如 72h")
		}
		opt.OlderThan = &d
	}
	st, err := store.Open(p.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	r, pruneErr := (&retention.Pruner{Store: st, Paths: p, OutputRoot: output, Options: opt}).Prune(context.Background())
	if *jsonOut {
		if err := printJSON(r); err != nil {
			return err
		}
	} else {
		action := "已清理"
		if *dryRun {
			action = "将清理"
		}
		for _, j := range r.Jobs {
			fmt.Printf("%s #%d %s：%.2f MiB（%s）\n", action, j.JobID, j.Name, float64(j.Bytes)/(1<<20), j.Reason)
		}
		fmt.Printf("%s %d 个任务、%d 个未引用环境文件，共 %.2f MiB。任务记录、标星任务和训练产物保留。\n", action, len(r.Jobs), len(r.Envs), float64(r.Bytes)/(1<<20))
	}
	return pruneErr
}
