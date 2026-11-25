import assert from 'node:assert/strict'
import test from 'node:test'
import { now, observeServerDate } from '../src/format.ts'

const skew = () => now() - Date.now() / 1000
// 服务器时钟比本机快 aheadMs 时，一次往返 rttMs 的请求收到的 Date 头
const observe = (aheadMs, rttMs = 20) => {
  const sent = Date.now()
  observeServerDate(new Date(sent + rttMs / 2 + aheadMs).toUTCString(), sent, sent + rttMs)
}

// 浏览器时钟不准时，时长和"数据 N 秒没有更新"都按服务器时间算
test('按响应头 Date 校正时钟差', () => {
  assert.equal(skew(), 0)
  observeServerDate(null, 0, 10)
  observeServerDate('not a date', 0, 10)
  assert.equal(skew(), 0)

  observe(60_000)
  assert.ok(Math.abs(skew() - 60) <= 1, `服务器快 60 秒: ${skew()}`)

  // 中位数：偶尔一个很慢或很怪的响应不会把时钟带偏
  for (let i = 0; i < 4; i++) observe(60_000)
  observe(-300_000)
  assert.ok(Math.abs(skew() - 60) <= 1, `一个离群值不应改变估计: ${skew()}`)

  // Date 只精确到秒：差 2 秒以内当作没差
  for (let i = 0; i < 9; i++) observe(800)
  assert.equal(skew(), 0)
  for (let i = 0; i < 9; i++) observe(-30_000)
  assert.ok(Math.abs(skew() + 30) <= 1, `本机快 30 秒: ${skew()}`)
})
