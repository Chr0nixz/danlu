//go:build linux

package strict

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const sudoersDir = "/etc/sudoers.d"

// 文件名里不能有 "."：sudo 的 #includedir 会跳过带点的文件，临时文件正好用这一点
func sudoersPath(uid int) string { return filepath.Join(sudoersDir, "danlu-gpu-"+strconv.Itoa(uid)) }

var safeUserRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

// Target 是要授权的用户：默认是 sudo 的调用者，name 可为空（LDAP 之类查不到名字时用 #uid）。
type Target struct {
	UID  int
	Name string
}

// ResolveTarget 解析 --user；为空时用 SUDO_UID/SUDO_USER。
func ResolveTarget(flagUser string) (Target, error) {
	if flagUser == "" {
		uid, err := sudoUID()
		if err != nil {
			return Target{}, errors.New("请用要跑 danlu 的那个用户执行 sudo danlu strict …，或者加 --user 用户名")
		}
		return Target{UID: uid, Name: os.Getenv("SUDO_USER")}, nil
	}
	if uid, err := strconv.Atoi(flagUser); err == nil && uid > 0 {
		t := Target{UID: uid}
		if u, err := user.LookupId(flagUser); err == nil {
			t.Name = u.Username
		}
		return t, nil
	}
	u, err := user.Lookup(flagUser)
	if err != nil {
		return Target{}, fmt.Errorf("找不到用户 %s：换成 --user <uid>，或者以该用户执行 sudo danlu strict …", flagUser)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid <= 0 {
		return Target{}, fmt.Errorf("用户 %s 不能是 root", flagUser)
	}
	return Target{UID: uid, Name: u.Username}, nil
}

// sudoersUser 是规则里的用户写法：名字安全时写名字，否则写 #uid
func (t Target) sudoersUser() string {
	if safeUserRE.MatchString(t.Name) {
		return t.Name
	}
	return "#" + strconv.Itoa(t.UID)
}

// Install 以 root 安装助手和 sudoers 规则。self 是当前 danlu 程序的路径。
func Install(t Target, self string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("要用 sudo 运行：sudo " + self + " strict install")
	}
	dir := filepath.Dir(HelperPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := rootOnly(d); err != nil {
			return err
		}
	}
	if err := copyFile(self, HelperPath, 0o755); err != nil {
		return fmt.Errorf("安装助手失败: %w", err)
	}
	fmt.Fprintf(out, "已安装助手 %s\n", HelperPath)

	dst := sudoersPath(t.UID)
	tmp := dst + ".tmp"
	defer os.Remove(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// 临时文件名带点，sudo 的 #includedir 不读它：校验通过之前规则不会生效
	_, err := pickSudoersRule(t.UID, t.sudoersUser(), func(rule string) ([]byte, error) {
		if err := os.WriteFile(tmp, []byte(rule), 0o440); err != nil {
			return nil, err
		}
		if err := os.Chmod(tmp, 0o440); err != nil {
			return nil, err
		}
		return runSbin(ctx, "visudo", "-cf", tmp)
	})
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Fprintf(out, "已写入 %s\n", dst)
	// 确认规则真的生效：用户名写错之类的情况 visudo 不会报错，但规则什么也不授权
	if b, err := runSbin(ctx, "sudo", "-n", "-l", "-U", t.sudoersUser(), HelperPath, "check"); err != nil {
		return fmt.Errorf("规则写进去了，但 sudo 没有给 %s 放行助手: %v %s", t.sudoersUser(), err, clip(b))
	}
	b, err := runSbin(ctx, HelperPath, "check")
	if err != nil {
		return fmt.Errorf("助手自检失败: %v %s", err, clip(b))
	}
	fmt.Fprintf(out, "自检：%s\n", strings.TrimSpace(string(b)))
	fmt.Fprintln(out, "完成。在面板「设置 → 调度」里打开严格限卡；只影响之后启动的任务。")
	return nil
}

// Uninstall 删除这个用户的 sudoers 规则；没有别的用户在用时连助手一起删。
func Uninstall(t Target, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("要用 sudo 运行：sudo danlu strict uninstall")
	}
	if err := os.Remove(sudoersPath(t.UID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(out, "已删除 %s\n", sudoersPath(t.UID))
	rest, _ := filepath.Glob(filepath.Join(sudoersDir, "danlu-gpu-*"))
	var others []string
	for _, f := range rest {
		if !strings.Contains(filepath.Base(f), ".") {
			others = append(others, f)
		}
	}
	if len(others) > 0 {
		fmt.Fprintf(out, "还有 %d 个用户在用严格限卡，保留助手 %s\n", len(others), HelperPath)
		return nil
	}
	if err := os.Remove(HelperPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(out, "已删除助手 %s\n", HelperPath)
	return nil
}

// rootOnly 确认目录属于 root、组和其他人不能写：否则普通用户能把助手换掉
func rootOnly(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid != 0 || st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s 必须属于 root 且组和其他人不能写", dir)
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// runSbin 执行系统目录里的命令（或绝对路径），环境清空
func runSbin(ctx context.Context, name string, args ...string) ([]byte, error) {
	path := name
	if !filepath.IsAbs(name) {
		path = ""
		for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				path = filepath.Join(dir, name)
				break
			}
		}
		if path == "" {
			return nil, fmt.Errorf("找不到 %s", name)
		}
	}
	return runPath(ctx, path, args...)
}
