import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { setLocale } from '../src/i18n.ts'
import { exitCodeOnly, failReasonText, failText, queuePauseText } from '../src/format.ts'

afterEach(() => setLocale('zh-CN'))

const job = (extra) => ({
  id: 1, name: 'vit', status: 'failed', exit_code: null, fail_reason: '',
  gpus_req: 1, gpus_pinned: [], gpus_assigned: [], cancel_requested: false,
  ...extra,
})

test('failReasonText：按 fail_kind/fail_detail 合成，zh 与服务器原文一致', () => {
  assert.equal(failReasonText(job({ fail_kind: 'cuda_oom' })), 'CUDA 显存不足')
  assert.equal(failReasonText(job({ fail_kind: 'cuda_oom', ext_seen: true })), 'CUDA 显存不足，运行期间卡上出现过外部进程')
  assert.equal(failReasonText(job({ fail_kind: 'host_oom', fail_detail: 'confirmed' })), '主机内存不足，被系统杀掉')
  assert.equal(failReasonText(job({ fail_kind: 'host_oom', fail_detail: 'worker' })), '运行期间本机发生过 OOM kill')
  assert.equal(failReasonText(job({ fail_kind: 'host_oom', fail_detail: 'guess137' })), '退出码 137，可能是主机内存不足')
  assert.equal(failReasonText(job({ fail_kind: 'signal', exit_code: 137 })), '被信号终止（SIGKILL）')
  assert.equal(failReasonText(job({ fail_kind: 'signal', fail_detail: 'SIGTERM', exit_code: 143 })), '被信号终止（SIGTERM）')
  assert.equal(failReasonText(job({ fail_kind: 'signal', exit_code: 130 })), '被信号终止（SIGINT）')
  assert.equal(failReasonText(job({ fail_kind: 'signal', exit_code: 150 })), '被信号终止（信号 22）')
  assert.equal(failReasonText(job({ fail_kind: 'exit', exit_code: 2 })), '退出码 2')
  assert.equal(failReasonText(job({ fail_kind: 'reboot' })), '服务器重启，任务中断')
  assert.equal(failReasonText(job({ fail_kind: 'killed' })), '进程被外部终止（没有留下退出码）')
  assert.equal(failReasonText(job({ fail_kind: 'nccl' })), 'NCCL 通信超时或出错')
  assert.equal(failReasonText(job({ fail_kind: 'exception' })), 'Python 异常')
  // start：译前缀后附原始错误；旧服务器没有 fail_detail 时回退原文
  assert.equal(failReasonText(job({ fail_kind: 'start', fail_detail: 'fork 失败', fail_reason: '启动失败: fork 失败' })), '启动失败: fork 失败')
  assert.equal(failReasonText(job({ fail_kind: 'start', fail_reason: '写入任务清单失败: x' })), '写入任务清单失败: x')
  assert.equal(failReasonText(job({ fail_kind: 'start' })), '任务启动失败')
  // 旧记录：无 kind 回退原文；host_oom 无 detail 回退原文
  assert.equal(failReasonText(job({ fail_reason: '旧版原因' })), '旧版原因')
  assert.equal(failReasonText(job({ fail_kind: 'host_oom', fail_reason: '主机内存不足，被系统杀掉' })), '主机内存不足，被系统杀掉')
  assert.equal(failReasonText(job({ exit_code: 3 })), '退出码 3')
  assert.equal(failReasonText(job({})), '失败')
})

test('failReasonText：英文合成', () => {
  setLocale('en')
  assert.equal(failReasonText(job({ fail_kind: 'cuda_oom', ext_seen: true })), 'CUDA out of memory; an external process appeared on the card while running')
  assert.equal(failReasonText(job({ fail_kind: 'host_oom', fail_detail: 'guess137' })), 'Exit code 137; possibly host out of memory')
  assert.equal(failReasonText(job({ fail_kind: 'signal', exit_code: 137 })), 'Terminated by signal (SIGKILL)')
  assert.equal(failReasonText(job({ fail_kind: 'exit', exit_code: 2 })), 'exit code 2')
  assert.equal(failReasonText(job({ fail_kind: 'reboot' })), 'Server rebooted; the job was interrupted')
  assert.equal(failReasonText(job({ fail_kind: 'start', fail_detail: 'exec: no such file', fail_reason: '启动失败: exec: no such file' })), 'Failed to start: exec: no such file')
  // 旧记录回退原文（中文数据不翻译）
  assert.equal(failReasonText(job({ fail_reason: '旧版原因' })), '旧版原因')
})

test('failText：原因加报错行', () => {
  assert.equal(failText(job({ fail_kind: 'exit', exit_code: 1, error_line: 'ValueError: bad' })), '退出码 1 · ValueError: bad')
  setLocale('en')
  assert.equal(failText(job({ fail_kind: 'exit', exit_code: 1, error_line: 'ValueError: bad' })), 'exit code 1 · ValueError: bad')
})

test('exitCodeOnly：机器码优先，旧服务器回退原文匹配', () => {
  assert.equal(exitCodeOnly(job({ fail_kind: 'exit', exit_code: 1 })), true)
  assert.equal(exitCodeOnly(job({ fail_kind: 'cuda_oom' })), false)
  assert.equal(exitCodeOnly(job({ fail_reason: '退出码 1' })), true)
  assert.equal(exitCodeOnly(job({ fail_reason: '退出码 -1' })), true)
  assert.equal(exitCodeOnly(job({ fail_reason: 'CUDA 显存不足' })), false)
  assert.equal(exitCodeOnly(job({})), true)
})

test('queuePauseText：按 kind 合成，旧服务器回退原文', () => {
  assert.equal(queuePauseText({ paused: false }), '')
  assert.equal(queuePauseText({ paused: true, kind: 'manual', reason: '手动暂停' }), '手动暂停')
  assert.equal(queuePauseText({ paused: true, kind: 'disk', reason: '旧原文' }), '旧原文')
  assert.equal(queuePauseText({ paused: true, kind: 'disk', disk_available_gb: 12.3, disk_min_gb: 20 }), '数据目录所在分区仅剩 12.3 GiB（暂停线 20 GiB）')
  assert.equal(queuePauseText({ paused: true, kind: 'fail_fast', job_id: 7, fail_seconds: 4, reason: '旧原文' }), '任务 #7 启动 4 秒后失败，已暂停启动后续任务')
  assert.equal(queuePauseText({ paused: true, kind: 'fail_fast', job_id: 7, reason: '旧原文' }), '任务 #7 快速失败，已暂停启动后续任务')
  assert.equal(queuePauseText({ paused: true, reason: '未知类型原文' }), '未知类型原文')
  setLocale('en')
  assert.equal(queuePauseText({ paused: true, kind: 'manual' }), 'Paused manually')
  assert.equal(queuePauseText({ paused: true, kind: 'disk', disk_available_gb: 12.3, disk_min_gb: 20 }), 'The data partition has only 12.3 GiB free (pause line 20 GiB)')
  assert.equal(queuePauseText({ paused: true, kind: 'fail_fast', job_id: 7, fail_seconds: 4 }), 'Job #7 failed 4s after starting; launching further jobs is paused')
  // 旧服务器英文界面回退中文原文（数据）
  assert.equal(queuePauseText({ paused: true, kind: 'disk', reason: '旧原文' }), '旧原文')
})