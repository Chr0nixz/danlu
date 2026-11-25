import { useSyncExternalStore } from 'react'
import { now } from './format.ts'

// 整页共用一个每秒一跳的定时器，没有组件在用时停掉
const listeners = new Set<() => void>()
let tick = 0
let timer: ReturnType<typeof setInterval> | undefined

function subscribe(fn: () => void) {
  listeners.add(fn)
  timer ??= setInterval(() => {
    tick++
    for (const l of listeners) l()
  }, 1000)
  return () => {
    listeners.delete(fn)
    if (!listeners.size && timer !== undefined) {
      clearInterval(timer)
      timer = undefined
    }
  }
}
const idle = () => () => {}
const currentTick = () => tick
const noTick = () => 0

/**
 * 每秒重渲染一次，返回现在的时间（unix 秒，按服务器时钟校正过，见 now）。
 * 等待时长、无输出时长、倒计时这些在渲染时现算的文字靠它走动：轮询结果没变时组件不会重渲染。
 * enabled 为 false 时不订阅，比如已经结束的任务行
 */
export function useNow(enabled = true): number {
  useSyncExternalStore(enabled ? subscribe : idle, enabled ? currentTick : noTick, noTick)
  return now()
}
