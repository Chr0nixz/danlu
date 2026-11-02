import assert from 'node:assert/strict'
import test from 'node:test'
import { progressRemaining } from '../src/format.ts'

test('解析 tqdm 的时分格式', () => {
  assert.equal(progressRemaining('84%|████▍     | 84/100 [01:23<02:34, 1.2it/s]'), '2:34')
})

test('解析 tqdm 的时分秒格式并补齐分钟和秒', () => {
  assert.equal(progressRemaining('10%|█         | 10/100 [00:10<01:02:03, 2it/s]'), '1:02:03')
  assert.equal(progressRemaining('10%|█         | 10/100 [00:10<1:2:3, 2it/s]'), '1:02:03')
})

test('解析超过一天的剩余时间', () => {
  assert.equal(progressRemaining(' 0%|          | 0/100 [00:00<2d 01:02:03, ?it/s]'), '2天1:02:03')
})

test('未知或没有 tqdm 进度条时不显示估计', () => {
  assert.equal(progressRemaining(' 0%|          | 0/100 [00:00<?, ?it/s]'), null)
  assert.equal(progressRemaining('普通训练输出 [loss<0.4, lr=1e-4]'), null)
  assert.equal(progressRemaining('没有进度条'), null)
})

test('同一行中挑选 tqdm 进度条而忽略普通尖括号字段', () => {
  assert.equal(
    progressRemaining('[epoch 1] [unknown<maybe, value] 20%|██        | 20/100 [00:03<00:07, 4it/s]'),
    '0:07',
  )
  assert.equal(
    progressRemaining('20%|██        | 20/100 [00:03<00:07, 4it/s] later [other<value, metadata]'),
    '0:07',
  )
})
