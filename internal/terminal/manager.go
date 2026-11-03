// Package terminal 管理面板里的交互式 shell 会话。
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	MaxSessions     = 3
	BufferSize      = 256 << 10
	SessionLifetime = 15 * time.Minute
	outputChunkSize = 16 << 10
)

var (
	ErrLimit       = errors.New("最多同时打开 3 个终端")
	ErrNotFound    = errors.New("终端会话不存在")
	ErrReplaced    = errors.New("终端已在其他页面连接")
	ErrClosed      = errors.New("终端管理器已关闭")
	ErrUnsupported = errors.New("当前系统不支持网页终端")
)

type OpenOptions struct {
	Workdir   string
	CondaEnv  string
	CondaBase string
}

type SessionInfo struct {
	ID       string `json:"id"`
	Workdir  string `json:"workdir"`
	OpenedAt int64  `json:"opened_at"`
	Attached bool   `json:"attached"`
}

type process interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type session struct {
	info    SessionInfo
	process process
	// attached 是当前连接，由 Manager.mu 保护
	attached     *Attachment
	lastDetached time.Time
	// mu 保护 output 和 changed；每次追加输出都关闭 changed 并换一个新的，唤醒所有等待者
	mu       sync.Mutex
	output   outputBuffer
	changed  chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newSession(id, workdir string, p process, now time.Time) *session {
	return &session{
		info:    SessionInfo{ID: id, Workdir: workdir, OpenedAt: now.Unix()},
		process: p, lastDetached: now,
		changed: make(chan struct{}), done: make(chan struct{}),
	}
}

type outputBuffer struct {
	data  []byte
	start uint64
}

func (b *outputBuffer) append(p []byte) {
	if len(p) == 0 {
		return
	}
	if len(p) >= BufferSize {
		b.start += uint64(len(b.data) + len(p) - BufferSize)
		b.data = append(b.data[:0], p[len(p)-BufferSize:]...)
		return
	}
	if overflow := len(b.data) + len(p) - BufferSize; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
		b.start += uint64(overflow)
	}
	b.data = append(b.data, p...)
}

func (b *outputBuffer) end() uint64 { return b.start + uint64(len(b.data)) }

// readAfter 返回从 offset 开始的输出和这段输出的实际起点。offset 已被覆盖时从缓冲开头读，起点会大于 offset。
func (b *outputBuffer) readAfter(offset uint64, limit int) ([]byte, uint64) {
	offset = min(max(offset, b.start), b.end())
	start := int(offset - b.start)
	stop := min(len(b.data), start+limit)
	return append([]byte(nil), b.data[start:stop]...), offset
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	pending  int
	closed   bool
	now      func() time.Time
	stop     chan struct{}
	reaper   sync.WaitGroup
	workers  sync.WaitGroup
	close    sync.Once
}

func NewManager() *Manager {
	return newManager(time.Now)
}

func newManager(now func() time.Time) *Manager {
	m := &Manager{
		sessions: make(map[string]*session), now: now,
		stop: make(chan struct{}),
	}
	m.reaper.Add(1)
	go m.reapLoop()
	return m
}

func (m *Manager) Open(opt OpenOptions) (SessionInfo, error) {
	if err := opt.validate(); err != nil {
		return SessionInfo{}, err
	}
	if !filepath.IsAbs(opt.Workdir) {
		return SessionInfo{}, errors.New("工作目录必须是绝对路径")
	}
	fi, err := os.Stat(opt.Workdir)
	if err != nil || !fi.IsDir() {
		return SessionInfo{}, fmt.Errorf("工作目录不可用: %s", opt.Workdir)
	}
	if !validCondaEnv(opt.CondaEnv) {
		return SessionInfo{}, errors.New("conda 环境只能是环境名（字母、数字、下划线、点和短横线）或绝对路径")
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionInfo{}, ErrClosed
	}
	if len(m.sessions)+m.pending >= MaxSessions {
		m.mu.Unlock()
		return SessionInfo{}, ErrLimit
	}
	m.pending++
	m.workers.Add(1)
	m.mu.Unlock()
	defer m.workers.Done()

	p, err := startPTY(opt)
	if err != nil {
		m.mu.Lock()
		m.pending--
		m.mu.Unlock()
		return SessionInfo{}, err
	}
	id, err := newID()
	if err != nil {
		_ = p.Close()
		m.mu.Lock()
		m.pending--
		m.mu.Unlock()
		return SessionInfo{}, err
	}
	s := newSession(id, opt.Workdir, p, m.now())

	m.mu.Lock()
	m.pending--
	if m.closed {
		m.mu.Unlock()
		_ = p.Close()
		return SessionInfo{}, ErrClosed
	}
	m.sessions[id] = s
	m.workers.Add(1)
	m.mu.Unlock()
	go m.readOutput(s)
	return s.info, nil
}

func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OpenedAt == out[j].OpenedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].OpenedAt < out[j].OpenedAt
	})
	return out
}

// Attach 连上会话，从 offset 开始补发输出；offset 为 0 时补发整个缓冲。
// 已有连接时由新连接接替：手机切后台或换网络后，旧连接常常已经断了，服务器却还没发现。
func (m *Manager) Attach(id string, offset uint64) (*Attachment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil, ErrNotFound
	}
	if s.attached != nil {
		close(s.attached.replaced)
	}
	a := &Attachment{manager: m, session: s, replaced: make(chan struct{})}
	s.attached = a
	s.info.Attached = true
	s.mu.Lock()
	a.Start = min(max(offset, s.output.start), s.output.end())
	s.mu.Unlock()
	return a, nil
}

// Exists 报告会话是否还在。
func (m *Manager) Exists(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id] != nil
}

func (m *Manager) CloseSession(id string) (SessionInfo, bool) {
	m.mu.Lock()
	s := m.sessions[id]
	if s != nil {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if s == nil {
		return SessionInfo{}, false
	}
	s.stop()
	return s.info, true
}

func (m *Manager) reapLoop() {
	defer m.reaper.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.reapExpired()
		}
	}
}

func (m *Manager) reapExpired() {
	now := m.now()
	m.mu.Lock()
	var expired []*session
	for id, s := range m.sessions {
		if !s.info.Attached && now.Sub(s.lastDetached) >= SessionLifetime {
			delete(m.sessions, id)
			expired = append(expired, s)
		}
	}
	m.mu.Unlock()
	for _, s := range expired {
		s.stop()
	}
}

func (m *Manager) readOutput(s *session) {
	defer m.workers.Done()
	buf := make([]byte, 32<<10)
	for {
		n, err := s.process.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.output.append(buf[:n])
			close(s.changed)
			s.changed = make(chan struct{})
			s.mu.Unlock()
		}
		if err != nil {
			m.mu.Lock()
			if m.sessions[s.info.ID] == s {
				delete(m.sessions, s.info.ID)
			}
			m.mu.Unlock()
			s.stop()
			return
		}
	}
}

func (s *session) stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		_ = s.process.Close()
	})
}

func (m *Manager) Close() {
	m.close.Do(func() {
		m.mu.Lock()
		m.closed = true
		close(m.stop)
		all := make([]*session, 0, len(m.sessions))
		for _, s := range m.sessions {
			all = append(all, s)
		}
		clear(m.sessions)
		m.mu.Unlock()
		for _, s := range all {
			s.stop()
		}
		m.workers.Wait()
		m.reaper.Wait()
	})
}

// validCondaEnv 接受环境名或绝对路径：不在 <base>/envs 下的环境（比如 ~/.conda/envs/x）在 conda env list 里只有路径。
func validCondaEnv(env string) bool {
	if strings.HasPrefix(env, "/") {
		return !strings.ContainsFunc(env, unicode.IsControl)
	}
	for _, c := range env {
		if !safeShellChar(c) || c == '/' {
			return false
		}
	}
	return true
}

func safeShellChar(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' || c == '/'
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

type Attachment struct {
	manager  *Manager
	session  *session
	replaced chan struct{}
	// Start 是补发的起点，即这个连接收到的第一个字节在会话输出里的偏移
	Start uint64
}

func (a *Attachment) active() error {
	select {
	case <-a.replaced:
		return ErrReplaced
	case <-a.session.done:
		return io.EOF
	default:
		return nil
	}
}

func (a *Attachment) Write(p []byte) error {
	if err := a.active(); err != nil {
		return err
	}
	_, err := a.session.process.Write(p)
	return err
}

func (a *Attachment) Resize(cols, rows int) error {
	if cols < 1 || cols > 500 || rows < 1 || rows > 200 {
		return errors.New("终端尺寸无效")
	}
	if err := a.active(); err != nil {
		return err
	}
	return a.session.process.Resize(cols, rows)
}

// Next 等到 offset 之后有输出时返回这段输出和它的起点；起点大于 offset 说明中间的输出已被缓冲覆盖。
func (a *Attachment) Next(ctx context.Context, offset uint64) ([]byte, uint64, error) {
	s := a.session
	for {
		select {
		case <-a.replaced:
			return nil, offset, ErrReplaced
		default:
		}
		s.mu.Lock()
		data, from := s.output.readAfter(offset, outputChunkSize)
		changed := s.changed
		s.mu.Unlock()
		if len(data) > 0 {
			return data, from, nil
		}
		select {
		case <-ctx.Done():
			return nil, offset, ctx.Err()
		case <-a.replaced:
			return nil, offset, ErrReplaced
		case <-s.done:
			// shell 退出前的最后一段输出可能和结束信号同时到，先读完再报告结束
			s.mu.Lock()
			more := s.output.end() > offset
			s.mu.Unlock()
			if !more {
				return nil, offset, io.EOF
			}
		case <-changed:
		}
	}
}

func (a *Attachment) Detach() {
	m := a.manager
	m.mu.Lock()
	if s := a.session; s.attached == a {
		s.attached = nil
		s.info.Attached = false
		s.lastDetached = m.now()
	}
	m.mu.Unlock()
}

// shellWord 只在需要时加引号，常见的环境名和路径在终端里显示得和手敲的一样。
func shellWord(s string) string {
	for _, c := range s {
		if !safeShellChar(c) {
			return shellQuote(s)
		}
	}
	return s
}

func (o OpenOptions) validate() error {
	if strings.IndexByte(o.Workdir, 0) >= 0 {
		return errors.New("工作目录包含无效字符")
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
