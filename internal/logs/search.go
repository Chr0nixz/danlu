package logs

import (
	"context"
	"errors"
	"io"
	"os"
	"unicode"
	"unicode/utf8"
)

const searchBlock = 64 << 10
const searchPreview = 512
const searchContext = 384

type SearchMatch struct {
	Offset int64  `json:"offset"`
	Text   string `json:"text"`
}

type SearchResult struct {
	Matches   []SearchMatch `json:"matches"`
	Scanned   int64         `json:"scanned"`
	Size      int64         `json:"size"`
	Truncated bool          `json:"truncated"`
}

// Search 只读打开时可见的文件范围。publish 收到只读的进度快照，可用于超时时返回已确认的结果。
func Search(ctx context.Context, path, query string, limit int, maxBytes int64, final bool, publish func(SearchResult)) (SearchResult, error) {
	result := SearchResult{Matches: make([]SearchMatch, 0, max(0, limit))}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("日志不是普通文件")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return result, err
	}
	result.Size = info.Size()
	if publish != nil {
		publish(result)
	}
	if query == "" || limit <= 0 {
		return result, nil
	}
	end := min(result.Size, maxBytes)
	line := newLineSearch(query)
	buf := make([]byte, searchBlock)
	var start int64
	addMatch := func(finalRune bool) {
		if preview, ok := line.finish(finalRune); ok {
			result.Matches = append(result.Matches, SearchMatch{Offset: start, Text: preview})
		}
	}
	for result.Scanned < end && ctx.Err() == nil {
		n, readErr := f.Read(buf[:min(int64(len(buf)), end-result.Scanned)])
		for i, b := range buf[:n] {
			if b == '\n' {
				addMatch(true)
				line.reset()
				start = result.Scanned + int64(i) + 1
				if len(result.Matches) == limit {
					result.Scanned = start
					result.Truncated = start < result.Size
					return result, nil
				}
			} else {
				line.byte(b)
			}
		}
		result.Scanned += int64(n)
		if publish != nil {
			publish(result)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return result, readErr
		}
		if n == 0 || readErr != nil {
			break
		}
	}
	result.Truncated = result.Scanned < result.Size
	if !result.Truncated && start < result.Size {
		if !line.cr {
			addMatch(final)
		}
	}
	return result, nil
}

// lineSearch 在当前可见段上做流式匹配。回车会废弃前一段，超长行只保留命中附近的文字。
type lineSearch struct {
	word       []rune
	prefix     []int
	matched    int
	recent     [searchContext]rune
	seen       int
	preview    []rune
	found      bool
	head, tail bool
	cr         bool
	ansi       byte
	pending    [utf8.UTFMax]byte
	npending   int
}

func newLineSearch(query string) *lineSearch {
	word := []rune(query)
	for i, r := range word {
		word[i] = foldRune(r)
	}
	prefix := make([]int, len(word))
	for i, n := 1, 0; i < len(word); i++ {
		for n > 0 && word[i] != word[n] {
			n = prefix[n-1]
		}
		if word[i] == word[n] {
			n++
		}
		prefix[i] = n
	}
	return &lineSearch{word: word, prefix: prefix, preview: make([]rune, 0, searchPreview)}
}

func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}
	fold := r
	for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
		fold = min(fold, next)
	}
	return fold
}

func (s *lineSearch) reset() {
	s.matched, s.seen, s.npending, s.ansi = 0, 0, 0, 0
	s.preview = s.preview[:0]
	s.found, s.head, s.tail, s.cr = false, false, false, false
}

func (s *lineSearch) byte(b byte) {
	if s.cr {
		s.reset()
	}
	if b == '\r' {
		s.cr = true
		return
	}
	switch s.ansi {
	case 1: // ESC
		switch b {
		case '[':
			s.ansi = 2
		case ']':
			s.ansi = 3
		default:
			s.ansi = 0
		}
		return
	case 2: // CSI
		if b >= 0x40 && b <= 0x7e {
			s.ansi = 0
		}
		return
	case 3, 4: // OSC，以 BEL 或 ESC + 反斜杠结束
		if b == '\a' || (s.ansi == 4 && b == '\\') {
			s.ansi = 0
		} else if b == '\x1b' {
			s.ansi = 4
		} else {
			s.ansi = 3
		}
		return
	}
	if b == '\x1b' {
		s.ansi = 1
		return
	}
	if s.npending == 0 && b < utf8.RuneSelf {
		s.rune(rune(b))
		return
	}
	s.pending[s.npending] = b
	s.npending++
	for s.npending > 0 && utf8.FullRune(s.pending[:s.npending]) {
		r, n := utf8.DecodeRune(s.pending[:s.npending])
		s.rune(r)
		s.npending = copy(s.pending[:], s.pending[n:s.npending])
	}
}

func (s *lineSearch) rune(r rune) {
	if s.found {
		if len(s.preview) < searchPreview {
			s.preview = append(s.preview, r)
		} else {
			s.tail = true
		}
		return
	}
	s.recent[s.seen%searchContext] = r
	s.seen++
	fold := foldRune(r)
	for s.matched > 0 && fold != s.word[s.matched] {
		s.matched = s.prefix[s.matched-1]
	}
	if fold == s.word[s.matched] {
		s.matched++
	}
	if s.matched == len(s.word) {
		s.found = true
		start := max(0, s.seen-searchContext)
		s.head = start > 0
		for i := start; i < s.seen; i++ {
			s.preview = append(s.preview, s.recent[i%searchContext])
		}
	}
}

func (s *lineSearch) finish(finalRune bool) (string, bool) {
	if s.npending > 0 && finalRune {
		s.rune(utf8.RuneError)
	}
	if !s.found {
		return "", false
	}
	text := string(s.preview)
	if s.head {
		text = "…" + text
	}
	if s.tail {
		text += "…"
	}
	return text, true
}
