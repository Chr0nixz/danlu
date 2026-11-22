import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { createT, getLocale, registerMessages, resolveLocale, setLocale, subscribeLocale, translate } from '../src/i18n.ts'
import { duration, gpuText, progressRemaining, statusLabel, timeAgo, now } from '../src/format.ts'

afterEach(() => setLocale('zh-CN'))

test('resolveLocale：显式选择优先，auto 按系统语言，取不到按中文', () => {
  assert.equal(resolveLocale('en', 'zh-CN'), 'en')
  assert.equal(resolveLocale('zh-CN', 'en-US'), 'zh-CN')
  assert.equal(resolveLocale('auto', 'zh-TW'), 'zh-CN')
  assert.equal(resolveLocale('auto', 'ZH'), 'zh-CN')
  assert.equal(resolveLocale('auto', 'en-US'), 'en')
  assert.equal(resolveLocale('auto', 'fr-FR'), 'en')
  assert.equal(resolveLocale(null, null), 'zh-CN')
  assert.equal(resolveLocale('坏值', 'en-US'), 'en')
})

test('插值：替换占位符，缺参数保留原样', () => {
  registerMessages('zh-CN', { 'test.hello': '你好 {name}，{n} 个任务' })
  registerMessages('en', { 'test.hello': 'Hello {name}, {n} jobs' })
  assert.equal(translate('test.hello', { name: '丹炉', n: 3 }), '你好 丹炉，3 个任务')
  assert.equal(translate('test.hello', { name: '丹炉' }), '你好 丹炉，{n} 个任务')
  setLocale('en')
  assert.equal(translate('test.hello', { name: 'danlu', n: 3 }), 'Hello danlu, 3 jobs')
})

test('复数：英文按 Intl.PluralRules 选 one/other，中文只有 other', () => {
  registerMessages('zh-CN', { 'test.count': '{n} 个任务' })
  registerMessages('en', { 'test.count': { one: '{n} job', other: '{n} jobs' } })
  assert.equal(translate('test.count', { n: 1 }), '1 个任务')
  assert.equal(translate('test.count', { n: 5 }), '5 个任务')
  setLocale('en')
  assert.equal(translate('test.count', { n: 1 }), '1 job')
  assert.equal(translate('test.count', { n: 0 }), '0 jobs')
  assert.equal(translate('test.count', { n: 5 }), '5 jobs')
  // count 和 n 都认
  assert.equal(translate('test.count', { count: 1 }), '1 job')
})

test('缺键回退：当前语言没有回退中文，再没有返回键名', () => {
  registerMessages('zh-CN', { 'test.only_zh': '只有中文' })
  setLocale('en')
  assert.equal(translate('test.only_zh'), '只有中文')
  assert.equal(translate('test.no_such_key'), 'test.no_such_key')
})

test('setLocale 通知订阅者，重复设置不通知', () => {
  let n = 0
  const off = subscribeLocale(() => n++)
  setLocale('en')
  setLocale('en')
  setLocale('zh-CN')
  off()
  setLocale('en')
  assert.equal(n, 2)
  assert.equal(getLocale(), 'en')
})

test('createT：应用目录与 core 目录合并可用', () => {
  const zh = { 'app.only': '应用文案' }
  registerMessages('zh-CN', zh)
  registerMessages('en', { 'app.only': 'App message' })
  const t = createT(zh)
  assert.equal(t('app.only'), '应用文案')
  assert.equal(t('status.running'), '运行中')
  setLocale('en')
  assert.equal(t('app.only'), 'App message')
  assert.equal(t('status.running'), 'Running')
})

test('format：时长、状态、GPU、相对时间随语言切换', () => {
  assert.equal(duration(90061), '1天1小时')
  assert.equal(duration(3661), '1小时1分')
  assert.equal(duration(61), '1分1秒')
  assert.equal(duration(42), '42秒')
  assert.equal(statusLabel('running'), '运行中')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [0, 1], gpus_req: 2 }), '指定 GPU 0,1')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [], gpus_req: 2 }), '2 张卡')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [], gpus_req: 0 }), '不用 GPU')
  assert.equal(timeAgo(now() - 120), '2 分钟前')
  assert.equal(progressRemaining('0%| | 0/100 [00:00<2d 01:02:03, ?it/s]'), '2天1:02:03')

  setLocale('en')
  assert.equal(duration(90061), '1d 1h')
  assert.equal(duration(3661), '1h 1m')
  assert.equal(duration(61), '1m 1s')
  assert.equal(duration(42), '42s')
  assert.equal(statusLabel('running'), 'Running')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [0, 1], gpus_req: 2 }), 'GPU 0,1 (pinned)')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [], gpus_req: 1 }), '1 GPU')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [], gpus_req: 2 }), '2 GPUs')
  assert.equal(gpuText({ gpus_assigned: [], gpus_pinned: [], gpus_req: 0 }), 'No GPU')
  assert.equal(timeAgo(now() - 60), '1 minute ago')
  assert.equal(timeAgo(now() - 120), '2 minutes ago')
  assert.equal(progressRemaining('0%| | 0/100 [00:00<2d 01:02:03, ?it/s]'), '2d 1:02:03')
})