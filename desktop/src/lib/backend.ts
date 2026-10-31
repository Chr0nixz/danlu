// 和 Rust 层打交道：服务器列表、汇总数据、偏好、运维、经 Rust 转发的 API 请求。界面从不直接访问服务器。

import { ApiError, createApi, type Api, type GPU, type Job, type Me, type Transport } from '@danlu/core'
import { Channel, invoke } from '@tauri-apps/api/core'

export interface ServerView {
  name: string
  ssh: string
  base_url: string
  tls_mode: 'local-ca' | 'file' | 'off'
  ca_fingerprint: string
  /** 这台电脑在服务器上的设备令牌编号 */
  token_id: number
}

/** 一台服务器的汇总，由 Rust 层每 3 秒（只在托盘时 10 秒）刷新一次 */
export interface ServerSnap {
  name: string
  online: boolean
  error: string | null
  /** 最后一次成功拿到数据的时间（毫秒），0 表示从没连上过 */
  updated_at: number
  failing_since: number | null
  gpus: GPU[] | null
  gpu_error: string | null
  /** 运行中、排队和 24 小时内结束的任务 */
  jobs: Job[]
  me: Me | null
  /** 每张卡最近 5 分钟的利用率：[时间（毫秒）, 利用率] */
  util: { index: number; points: [number, number][] }[]
}

export interface Alert {
  key: string
  server: string
  kind: 'offline' | 'gpu_error' | 'external' | 'blocked'
  text: string
}

export interface FleetView {
  servers: ServerSnap[]
  alerts: Alert[]
  read: string[]
  polled_at: number
}

export interface Prefs {
  notify: boolean
  shortcut: string
  autostart: boolean
}

export interface Focus {
  kind: 'job' | 'server'
  key: string
}

export const listServers = () => invoke<ServerView[]>('list_servers')
export const sshHosts = () => invoke<string[]>('ssh_hosts')
export const addServer = (sshTarget: string, name: string) => invoke<ServerView>('add_server', { sshTarget, name })
/** 返回吊销令牌失败时的提示 */
export async function removeServer(name: string, revoke: boolean) {
  const warning = await invoke<string | null>('remove_server', { name, revoke })
  apis.delete(name)
  return warning
}

export const getFleet = () => invoke<FleetView>('get_fleet')
export const refreshFleet = () => invoke('refresh_fleet')
export const markRead = (keys: string[]) => invoke('mark_read', { keys })
export const getPrefs = () => invoke<Prefs>('get_prefs')
export const setPrefs = (prefs: Prefs) => invoke('set_prefs', { prefs })
export const openMain = (focus?: Focus) => invoke('open_main', { focus: focus ?? null })
export const appVersion = () => invoke<string>('app_version')

export interface UpgradeSource {
  arch: string
  path: string
  size: number
  modified: number
}
export interface UpgradeResult {
  ok: boolean
  from: string
  to: string
  problem: string | null
  journal: string | null
}
export interface ActionResult {
  command: string
  output: string
}
export type ServiceAction = 'restart' | 'logs' | 'entrance' | 'port' | 'cert' | 'password'

export const upgradeSources = () => invoke<UpgradeSource[]>('upgrade_sources')
export function upgradeServer(name: string, onProgress: (line: string) => void) {
  const ch = new Channel<string>()
  ch.onmessage = onProgress
  return invoke<UpgradeResult>('upgrade_server', { name, onProgress: ch })
}
export const serviceAction = (name: string, action: ServiceAction, value?: string) =>
  invoke<ActionResult>('service_action', { name, action, value: value ?? null })

type StreamMsg = { kind: 'event'; event: string; data: string } | { kind: 'closed'; error: string | null }

function transportFor(server: string): Transport {
  return {
    async request(method, path, body) {
      try {
        return await invoke<{ status: number; body: string }>('api_request', { server, method, path, body })
      } catch (e) {
        // Rust 层只在没连上时报错（网络、证书、超时），HTTP 错误码照常返回
        throw new ApiError(0, String(e))
      }
    },
    stream(path, h) {
      let id: number | null = null
      let closed = false
      const ch = new Channel<StreamMsg>()
      ch.onmessage = (m) => {
        if (closed) return
        if (m.kind === 'event') return h.onEvent(m.event, m.data)
        closed = true
        h.onError()
      }
      invoke<number>('api_stream', { server, path, onEvent: ch })
        .then((i) => {
          id = i
          if (closed) invoke('api_stream_close', { id })
        })
        .catch(() => {
          if (closed) return
          closed = true
          h.onError()
        })
      return () => {
        closed = true
        if (id !== null) invoke('api_stream_close', { id })
      }
    },
  }
}

const apis = new Map<string, Api>()

/** 每台服务器一个 API 对象。同一台始终返回同一个，可以放进 hook 的依赖里 */
export function apiFor(server: string): Api {
  let a = apis.get(server)
  if (!a) {
    a = createApi(transportFor(server), {
      emptyNotFound: `${server} 不认这台电脑的令牌了（可能已被吊销），请在「服务器」页删除后重新添加`,
    })
    apis.set(server, a)
  }
  return a
}

/** 把服务器上的文件（完整日志、代码快照）存进下载目录，返回保存的路径 */
export const saveFile = (server: string, path: string, filename: string) => invoke<string>('save_file', { server, path, filename })
export const revealFile = (path: string) => invoke('reveal_file', { path })
export const openExternal = (url: string) => invoke('open_external', { url })
