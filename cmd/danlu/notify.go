package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"danlu/internal/config"
	"danlu/internal/notify"
	"danlu/internal/store"

	qrcode "github.com/skip2/go-qrcode"
)

func cmdNotify(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：danlu notify setup [--server 地址] [--topic 名字] [--token-stdin] [--new-topic] [--disable] [--json] | test")
	}
	switch args[0] {
	case "setup":
		return cmdNotifySetup(args[1:])
	case "test":
		if len(args) != 1 {
			return errors.New("用法：danlu notify test")
		}
		p, c, err := loadForEdit()
		if err != nil {
			return err
		}
		st, err := store.Open(p.DB)
		if err != nil {
			return err
		}
		defer st.Close()
		hostname, _ := os.Hostname()
		sender := notify.New(st, c.Notify, hostname, c.BaseURL())
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := sender.Test(ctx); err != nil {
			return err
		}
		fmt.Println("测试推送已发送，请在 ntfy App 中确认收到。")
		return nil
	default:
		return errors.New("通知命令只有 setup 和 test")
	}
}

func cmdNotifySetup(args []string) error {
	fs := flag.NewFlagSet("notify setup", flag.ContinueOnError)
	server := fs.String("server", "", "ntfy 服务器地址（默认 https://ntfy.sh，也支持路径前缀）")
	topic := fs.String("topic", "", "使用已有 topic，默认生成随机名字")
	tokenStdin := fs.Bool("token-stdin", false, "从标准输入读取 ntfy 令牌，不放在命令行中")
	newTopic := fs.Bool("new-topic", false, "重新生成 topic；旧订阅需要更换")
	disable := fs.Bool("disable", false, "关闭推送")
	asJSON := fs.Bool("json", false, "以 JSON 输出订阅地址，不打印二维码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*disable && (*server != "" || *topic != "" || *tokenStdin || *newTopic)) || (*newTopic && *topic != "") {
		return errors.New("notify setup 参数不能同时指定关闭、修改 topic 或令牌；--new-topic 与 --topic 二选一")
	}
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	if *disable {
		c.Notify.NTFY, c.Notify.Token = "", ""
	} else {
		if *server != "" || *topic != "" || *newTopic || c.Notify.NTFY == "" {
			endpoint, err := notifyTopicURL(c.Notify.NTFY, *server, *topic, *newTopic)
			if err != nil {
				return err
			}
			// 换到另一个服务器时不沿用旧服务器的令牌。
			if *server != "" {
				c.Notify.Token = ""
			}
			c.Notify.NTFY = endpoint
		}
		if *tokenStdin {
			token, err := io.ReadAll(io.LimitReader(os.Stdin, 4098))
			if err != nil {
				return errors.New("读取 ntfy 令牌失败")
			}
			c.Notify.Token = strings.TrimSuffix(strings.TrimSuffix(string(token), "\n"), "\r")
		}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if err := config.Save(p, c); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"enabled": c.Notify.NTFY != "", "url": c.Notify.NTFY, "restart_required": true})
	}
	if c.Notify.NTFY == "" {
		fmt.Println("手机推送已关闭。")
	} else {
		qr, err := qrcode.New(c.Notify.NTFY, qrcode.Medium)
		if err != nil {
			return errors.New("配置已保存，但订阅地址过长，无法生成二维码；请查看 config.toml 的 notify.ntfy")
		}
		fmt.Println("在 ntfy App 中订阅此地址，或扫描二维码：")
		fmt.Println(c.Notify.NTFY)
		fmt.Print(qr.ToSmallString(false))
		if c.Notify.Token != "" {
			fmt.Println("此订阅启用了鉴权，请在 ntfy App 中配置对应服务器账号。")
		}
	}
	fmt.Println(restartHint)
	return nil
}

func notifyTopicURL(existing, server, topic string, rotate bool) (string, error) {
	if strings.ContainsAny(topic, "/\\%") {
		return "", errors.New("topic 只能包含字母、数字、下划线或连字符")
	}
	if server == "" && existing != "" {
		u, err := config.ParseNTFY(existing)
		if err != nil {
			return "", err
		}
		parts := strings.Split(u.Path, "/")
		if topic == "" && !rotate {
			topic = parts[len(parts)-1]
		}
		u.Path = strings.Join(parts[:len(parts)-1], "/")
		server = strings.TrimRight(u.String(), "/")
	}
	if server == "" {
		server = "https://ntfy.sh"
	}
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || u == nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("ntfy 服务器地址无效")
	}
	if topic == "" {
		topic = "danlu-" + rand.Text()
	}
	u.Path += "/" + topic
	endpoint := u.String()
	if _, err := config.ParseNTFY(endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}
