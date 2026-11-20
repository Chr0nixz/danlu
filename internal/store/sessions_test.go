package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGetSettings(t *testing.T) {
	s := openTest(t)
	if err := s.SetSettings(map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSettings("a", "b", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("GetSettings = %v", got)
	}
	if got, err := s.GetSettings(); err != nil || len(got) != 0 {
		t.Fatalf("不传键时应返回空结果，实际 %v %v", got, err)
	}
}

// 会话 id 只露出令牌哈希的前 16 位；注销按 id，当前会话单独标出
func TestListAndRevokeSessions(t *testing.T) {
	s := openTest(t)
	cur, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, h := range []string{cur, other} {
		if err := s.CreateSession(h, time.Hour, "10.0.0.1", "UA"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateSession(strings.Repeat("c", 64), -time.Minute, "", ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSessions(cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("过期会话不应列出，实际 %d 个", len(list))
	}
	for _, v := range list {
		if len(v.ID) != 16 || strings.Contains(cur, v.ID) != v.Current {
			t.Fatalf("会话 %+v 的 id 或当前标记不对", v)
		}
	}
	if err := s.DeleteSessionByID(SessionID(other)); err != nil {
		t.Fatal(err)
	}
	if s.SessionValid(other) || !s.SessionValid(cur) {
		t.Fatal("应只注销指定的会话")
	}
	if err := s.DeleteSessionByID(SessionID(other)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复注销应返回 ErrNotFound，实际 %v", err)
	}
	if err := s.DeleteSessionByID("short"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("id 长度不对应返回 ErrNotFound，实际 %v", err)
	}

	if err := s.CreateSession(other, time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteSessionsExceptCount(cur); err != nil || n != 1 {
		t.Fatalf("注销其他会话应注销 1 个，实际 %d %v", n, err)
	}
	if !s.SessionValid(cur) {
		t.Fatal("当前会话不应被注销")
	}
}

// 缩短有效期：其他会话从登录时算起，当前会话从现在算起；只提前，不延长
func TestClampSessions(t *testing.T) {
	s := openTest(t)
	cur, old, fresh := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, h := range []string{cur, old, fresh} {
		if err := s.CreateSession(h, 7*24*time.Hour, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	twoDaysAgo := time.Now().Add(-48 * time.Hour).Unix()
	if _, err := s.db.Exec("UPDATE sessions SET created_at=? WHERE token_hash IN (?,?)", twoDaysAgo, cur, old); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSecurity(map[string]string{"auth.session_ttl": "86400"}, 24*time.Hour, cur); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetSettings("auth.session_ttl"); got["auth.session_ttl"] != "86400" {
		t.Fatalf("设置应一起写入，实际 %v", got)
	}
	if s.SessionValid(old) {
		t.Fatal("两天前登录的会话在有效期改成 1 天后应失效")
	}
	if !s.SessionValid(cur) || !s.SessionValid(fresh) {
		t.Fatal("当前会话和刚登录的会话应仍然有效")
	}
	var exp int64
	if err := s.db.QueryRow("SELECT expires_at FROM sessions WHERE token_hash=?", cur).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if d := time.Until(time.Unix(exp, 0)); d > 24*time.Hour+time.Minute || d < 23*time.Hour {
		t.Fatalf("当前会话应从现在起按 1 天截止，实际还剩 %s", d)
	}
	// 改长不会延长已有会话
	if err := s.SaveSecurity(nil, 30*24*time.Hour, cur); err != nil {
		t.Fatal(err)
	}
	var exp2 int64
	if err := s.db.QueryRow("SELECT expires_at FROM sessions WHERE token_hash=?", cur).Scan(&exp2); err != nil || exp2 != exp {
		t.Fatalf("改长后到期时间不应变化：%d → %d %v", exp, exp2, err)
	}
}

// 收紧会话失败时，设置也不写入
func TestSaveSecurityIsAtomic(t *testing.T) {
	s := openTest(t)
	if err := s.CreateSession(strings.Repeat("a", 64), time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_clamp BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, '模拟写库失败'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.SaveSecurity(map[string]string{"auth.session_ttl": "600", "auth.entrance": "newentrance1"}, 10*time.Minute, "")
	if err == nil {
		t.Fatal("收紧会话失败时应返回错误")
	}
	if got, _ := s.GetSettings("auth.session_ttl", "auth.entrance"); len(got) != 0 {
		t.Fatalf("收紧会话失败时设置不应写入，实际 %v", got)
	}
}
