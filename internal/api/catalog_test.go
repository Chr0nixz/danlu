package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"danlu/internal/i18ntest"
)

// 错误码与前端 core 目录的同步检查（docs/I18N.md「后端错误码」）。
// 解析 internal/ 和 cmd/ 的源码，找出所有带字面量机器码的调用点：
//   - writeErrC(w, status, "code", msg, params)
//   - scheduler 的 invalid("code", params, format, args...)
//   - settings.InvalidC("code", msg, params)
// 逐个核对：目录里有 error.<code>；原文是字面量时 zh 与它逐字一致；
// 目录（zh 和 en）用到的占位符都在调用点的 params 里。范围/偏好的按项编码由 settings 包的测试核对。

// 客户端自己产生的错误（传输层），服务器不发
var clientOnlyCodes = []string{"network", "gate_expired", "token_revoked"}

type codeSite struct {
	pos         string
	code        string
	msg         string            // 字面量原文；不是字面量、或带格式化参数时为空
	params      map[string]string // 插值参数名 → 字面量值（值不是字面量时为空串）
	paramsKnown bool              // params 是字面量 map 或 nil；否则不检查占位符
}

// render 用调用点的字面量参数填目录模板；有占位符拿不到字面量值时返回 false（不做逐字比较）
func (s codeSite) render(tpl string) (string, bool) {
	ok := true
	out := regexp.MustCompile(`\{(\w+)\}`).ReplaceAllStringFunc(tpl, func(m string) string {
		v := s.params[m[1:len(m)-1]]
		if v == "" {
			ok = false
		}
		return v
	})
	return out, ok
}

func strLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// paramKeys 读 nil 或 map[string]any{"k": v, ...} 字面量：键 → 值的字面量文本（值不是数字/字符串字面量时为空串）
func paramKeys(e ast.Expr) (map[string]string, bool) {
	if id, ok := e.(*ast.Ident); ok && id.Name == "nil" {
		return map[string]string{}, true
	}
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	if _, isMap := cl.Type.(*ast.MapType); !isMap {
		return nil, false
	}
	keys := map[string]string{}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		k, ok := strLit(kv.Key)
		if !ok {
			return nil, false
		}
		keys[k] = ""
		if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.INT {
			keys[k] = lit.Value
		} else if s, ok := strLit(kv.Value); ok {
			keys[k] = s
		}
	}
	return keys, true
}

func funcName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// scanSource 返回错误码调用点和操作记录的动作码（.Audit(ip, "action", detail) 与 changeReservation("action", …)）
func scanSource(t *testing.T) (sites []codeSite, audits map[string]string) {
	t.Helper()
	root := i18ntest.RepoRoot(t)
	audits = map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			inScheduler := strings.Contains(filepath.ToSlash(path), "internal/scheduler/")
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				rel, _ := filepath.Rel(root, fset.Position(call.Pos()).Filename)
				pos := filepath.ToSlash(rel) + ":" + strconv.Itoa(fset.Position(call.Pos()).Line)
				a := call.Args
				var codeIdx, msgIdx, paramsIdx int
				exactArgs := -1 // 实参个数等于它时 msg 才是不带格式化参数的原文；-1 表示不限
				switch name := funcName(call); {
				case name == "writeErrC" && len(a) == 5:
					codeIdx, msgIdx, paramsIdx = 2, 3, 4
				case name == "invalid" && inScheduler && len(a) >= 3:
					codeIdx, paramsIdx, msgIdx, exactArgs = 0, 1, 2, 3
				case name == "InvalidC" && len(a) == 3:
					codeIdx, msgIdx, paramsIdx = 0, 1, 2
				case name == "Audit" && len(a) == 3:
					if action, ok := strLit(a[1]); ok {
						audits[action] = pos
					}
					return true
				case name == "changeReservation" && len(a) == 2:
					if action, ok := strLit(a[0]); ok {
						audits[action] = pos
					}
					return true
				default:
					return true
				}
				code, ok := strLit(a[codeIdx])
				if !ok {
					return true // 码是变量（writeErrE 的转发），由来源处覆盖
				}
				s := codeSite{pos: pos, code: code}
				if msg, ok := strLit(a[msgIdx]); ok && (exactArgs < 0 || len(a) == exactArgs) {
					s.msg = msg
				}
				s.params, s.paramsKnown = paramKeys(a[paramsIdx])
				sites = append(sites, s)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// 防止解析悄悄失效（比如函数改名）导致下面的检查空转
	if len(sites) < 60 || len(audits) < 20 {
		t.Fatalf("只找到 %d 个错误码调用点、%d 个操作记录动作，扫描规则可能过期了", len(sites), len(audits))
	}
	return sites, audits
}

func TestErrorCodesMatchCoreCatalog(t *testing.T) {
	zh, en := i18ntest.Core(t, "zh-CN"), i18ntest.Core(t, "en")
	sites, _ := scanSource(t)
	emitted := map[string]bool{}
	for _, s := range sites {
		emitted[s.code] = true
		key := "error." + s.code
		z, ok := zh[key]
		if !ok {
			t.Errorf("%s: core 目录缺 %s（新增错误码要同时加 zh/en 键）", s.pos, key)
			continue
		}
		if s.msg != "" {
			if want, ok := s.render(z); ok && want != s.msg {
				t.Errorf("%s: %s 与服务器原文不一致：\n  目录   %s\n  服务器 %s", s.pos, key, want, s.msg)
			}
		}
		if s.paramsKnown {
			for lang, text := range map[string]string{"zh": z, "en": en[key]} {
				for _, p := range i18ntest.Placeholders(text) {
					if _, ok := s.params[p]; !ok {
						t.Errorf("%s: %s 的 %s 文案用了 {%s}，但调用点的 params 里没有", s.pos, key, lang, p)
					}
				}
			}
		}
	}
	for _, sc := range sentinelCodes {
		emitted[sc.code] = true
		if z, ok := zh["error."+sc.code]; !ok {
			t.Errorf("哨兵错误 %v 的码 %s 在 core 目录里没有", sc.err, sc.code)
		} else if z != sc.err.Error() {
			t.Errorf("error.%s 与哨兵错误原文不一致：\n  目录   %s\n  服务器 %s", sc.code, z, sc.err.Error())
		}
	}
	// 反向：目录里不留服务器已经不发的码
	for _, code := range i18ntest.ErrorKeys(zh) {
		if emitted[code] || slices.Contains(clientOnlyCodes, code) ||
			strings.HasPrefix(code, "range_") || strings.HasPrefix(code, "pref_invalid_") {
			continue
		}
		t.Errorf("core 目录的 error.%s 没有服务器调用点（删掉它，或加进 clientOnlyCodes）", code)
	}
}

// 操作记录的动作名按码翻译（web/src/pages/settings/shared.tsx 的 actionKeys），没有映射就显示原始码
func TestAuditActionsHaveLabels(t *testing.T) {
	_, audits := scanSource(t)
	b, err := os.ReadFile(filepath.Join(i18ntest.RepoRoot(t), "web", "src", "pages", "settings", "shared.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+([a-z_]+): 'settings\.action\.`).FindAllStringSubmatch(string(b), -1) {
		labels[m[1]] = true
	}
	for action, pos := range audits {
		if !labels[action] {
			t.Errorf("%s: 操作记录动作 %s 在面板 actionKeys 里没有文案", pos, action)
		}
	}
}
