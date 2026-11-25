import assert from 'node:assert/strict'
import test from 'node:test'
import { LogRenderer, collapseCR, logToHtml } from '../src/terminal.ts'

const osc8 = (url, text) => `\x1b]8;;${url}\x07${text}\x1b]8;;\x07`

// 日志里的 OSC 8 链接不能把面板导航走：面板在新标签页打开、不带来源，桌面客户端只留文字
test('OSC 8 链接', () => {
  const line = `see ${osc8('https://example.com/a?b=1&c="x"', 'docs')}\n`
  const r = new LogRenderer()
  const { lines } = r.update(line)
  assert.match(lines, /<a href="https:\/\/example\.com\/a\?b=1&amp;c=&quot;x&quot;" target="_blank" rel="noopener noreferrer">docs<\/a>/)

  // 最后一行（还没换行）走状态副本，同样的画法
  const tail = new LogRenderer().update(`x ${osc8('https://example.com', 'go')}`).tail
  assert.match(tail, /target="_blank" rel="noopener noreferrer">go<\/a>/)

  const desktop = logToHtml(line)
  assert.ok(!desktop.includes('<a'), desktop)
  assert.ok(desktop.includes('docs'))

  // javascript: 之类的协议只留文字
  const evil = new LogRenderer().update(`${osc8('javascript:alert(1)', 'click <me>')}\n`).lines
  assert.ok(!evil.includes('<a') && !evil.includes('javascript'), evil)
  assert.ok(evil.includes('click &lt;me&gt;'), evil)
})

// 与 internal/logs/collapse_test.go 同一套用例
test('collapseCR', () => {
  const cases = {
    'plain\nlines\n': 'plain\nlines\n',
    'a\r\nb\r\n': 'a\nb\n',
    '\r 10%|█   |\r 50%|███ |\r100%|████|\n': '100%|████|\n',
    'epoch 1\r 30%\r 60%': ' 60%',
    'x\r': 'x',
    'x\r\r': 'x',
    'a\r\r\nb': 'a\nb',
    'epoch 1\r 30%\r': ' 30%',
    '\r\r\n': '\n',
    'a\rb\nc\rd\r\ne': 'b\nd\ne',
    '': '',
  }
  for (const [input, want] of Object.entries(cases)) assert.equal(collapseCR(input), want, JSON.stringify(input))

  // print(..., end="\r") 的最后一行在日志框里看得见
  assert.equal(new LogRenderer().update('loss 0.5\r').tail, 'loss 0.5')
})

// ESC [ 后面先是 ASCII、再是非 ASCII：ansi_up 会当成没收完的序列，之后的内容全卡住
test('收不完的转义序列不卡住日志', () => {
  const r = new LogRenderer()
  let out = r.update('a\x1b[12中文\n').lines
  assert.equal(out, 'a[12中文\n')
  out = r.update('a\x1b[12中文\nnext\n').lines
  assert.equal(out, 'next\n')
  assert.equal(r.update('a\x1b[12中文\nnext\ntail\x1b[\u00ff').tail, 'tail[\u00ff')

  // 合法序列后面紧跟中文照常上色
  assert.match(logToHtml('\x1b[31m红\x1b[0m\n'), /<span class="ansi-red-fg">红<\/span>/)
  // 只有 ASCII 的半截序列可能还没写完，留给下一次
  assert.equal(new LogRenderer().update('x\x1b[3').tail, 'x')
  assert.match(new LogRenderer().update('x\x1b[31mred').tail, /ansi-red-fg">red/)
})
