package main

import (
	"fmt"

	"danlu/internal/config"
	"danlu/internal/trace"
)

func cmdTrace(args []string) error {
	fs := newFlags("trace", "danlu trace <服务器上的绝对路径> [--json]")
	asJSON := fs.Bool("json", false, "以 JSON 输出（与 GET /api/trace 相同）")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("用法：danlu trace <服务器上的绝对路径> [--json]")
	}
	args = pos
	p, c, err := loadEffective()
	if err != nil {
		return err
	}
	root, err := config.ResolvePath(c.Runtime.OutputRoot)
	if err != nil {
		return err
	}
	st, err := openStore(p)
	if err != nil {
		return err
	}
	defer st.Close()

	result, err := trace.Search(st, root, p.Run, args[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(result)
	}
	fmt.Println("路径：", result.Path)
	if len(result.Matches) == 0 {
		fmt.Println("没有找到匹配的任务")
		return nil
	}
	for _, match := range result.Matches {
		job := match.Job
		fmt.Printf("\n#%d %s（%s，%s）\n", job.ID, job.Name, traceSourceLabel(match.Source), job.Status)
		fmt.Printf("  工作目录：%s\n", job.Workdir)
		fmt.Printf("  命令：%s\n", job.Cmd)
		if job.Memo != "" {
			fmt.Printf("  备注：%s\n", job.Memo)
		}
		if job.OutputDir != "" {
			fmt.Printf("  输出目录：%s\n", job.OutputDir)
		}
		fmt.Printf("  任务链接：%s/jobs/%d\n", c.BaseURL(), job.ID)
	}
	fmt.Printf("\n面板入口：%s\n", c.URL())
	return nil
}

func traceSourceLabel(source trace.Source) string {
	switch source {
	case trace.SourceOutputDir:
		return "输出目录"
	case trace.SourceRunCode:
		return "快照重跑目录"
	case trace.SourceManifest:
		return "danlu.json"
	default:
		return "可能是"
	}
}
