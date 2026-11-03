package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"danlu/internal/api"
	"danlu/internal/config"
	"danlu/internal/doctor"
)

// cmdDoctor 的文字输出有 fail 项时退出码为 1，方便在 shell 里判断；
// --json 只要体检跑完就返回 0，结论都在 JSON 里，客户端按非 0 退出码丢弃输出时也不会丢掉体检结果。
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "以 JSON 输出（有 fail 项时退出码仍为 0）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("用法: danlu doctor [--json]")
	}
	p := paths()
	c, err := config.Load(p)
	if err != nil {
		return err
	}
	report := doctor.Run(context.Background(), c, p, version, api.APIVersion)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	failed := 0
	for _, check := range report.Checks {
		label := map[string]string{"ok": "OK", "warn": "WARN", "fail": "FAIL"}[check.Level]
		fmt.Printf("[%s] %s：%s\n", label, check.Title, check.Detail)
		if check.Fix != "" {
			fmt.Printf("      建议：%s\n", check.Fix)
		}
		if check.Level == "fail" {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("体检发现 %d 项需要处理", failed)
	}
	return nil
}
