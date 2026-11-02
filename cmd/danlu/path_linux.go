package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const commandPathBlock = `# >>> danlu PATH >>>
case ":$PATH:" in
  *":$HOME/.local/bin:"*) ;;
  *) export PATH="$HOME/.local/bin:$PATH" ;;
esac
# <<< danlu PATH <<<

`

// 安装和升级时注册命令；PATH 配置失败不能阻止服务启动。
func setupCommandPath(exe string) {
	home, err := os.UserHomeDir()
	if err == nil {
		err = ensureCommandPath(home, os.Getenv("SHELL"), os.Getenv("ZDOTDIR"), exe)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 自动配置 danlu 命令失败：%v\n", err)
		return
	}
	bin := filepath.Join(home, ".local", "bin")
	if !strings.Contains(":"+os.Getenv("PATH")+":", ":"+bin+":") {
		_ = os.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	}
	fmt.Fprintln(os.Stderr, "✔ 已配置 danlu 命令。新终端可直接使用；当前终端执行：export PATH=\"$HOME/.local/bin:$PATH\"")
}

func ensureCommandPath(home, shell, zdotdir, exe string) error {
	link := filepath.Join(home, ".local", "bin", "danlu")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if exe != link {
		current, err := filepath.EvalSymlinks(link)
		switch {
		case err == nil && current == exe:
		case errors.Is(err, os.ErrNotExist):
			if err := os.Symlink(exe, link); err != nil {
				return fmt.Errorf("创建 %s 失败: %w", link, err)
			}
		case err != nil:
			return err
		default:
			return fmt.Errorf("%s 已存在且指向其他程序，请手动处理", link)
		}
	}
	files := []string{filepath.Join(home, ".profile"), filepath.Join(home, ".bashrc")}
	// Bash 登录时只读取最先存在的一个文件，已有的配置不能被 .profile 绕过。
	for _, name := range []string{".bash_profile", ".bash_login"} {
		path := filepath.Join(home, name)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if filepath.Base(shell) == "zsh" {
		if zdotdir == "" {
			zdotdir = home
		}
		files = append(files, filepath.Join(zdotdir, ".zshenv"))
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if strings.Contains(string(content), commandPathBlock) {
			continue
		}
		// 放在 .bashrc 的非交互 return 之前，ssh 主机 danlu ... 才能找到命令。
		if err := os.WriteFile(path, append([]byte(commandPathBlock), content...), 0o644); err != nil {
			return err
		}
	}
	return nil
}
