import { useQueryClient } from '@tanstack/react-query'
import { Component, type ErrorInfo, type ReactNode, useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router'
import { Button, buttonClass, Card, cx, Spinner } from './ui'

/**
 * 页面文件（assets/xxx-hash.js）加载失败。最常见的是丹炉升级以后：开着的标签页还按旧版本的文件名去要，
 * 新版本里没有这个文件，服务器回的是 index.html，动态 import 失败。各浏览器的报错写法不一样
 */
const chunkError = /dynamically imported module|Importing a module script failed|Unable to preload CSS|Failed to load module script/i
const isChunkError = (e: unknown) => e instanceof Error && chunkError.test(e.message)

const reloadKey = 'danlu.chunk-reload'

/**
 * 页面文件加载失败时自动刷新一次，拿到新版本的 index.html。10 秒内已经刷新过（新版本里也没有这个文件，
 * 或者网络断了），就不再刷，免得来回刷新；存储不可用时也不刷，交给用户点"重新加载"
 */
function reloadOnce() {
  try {
    if (Date.now() - Number(sessionStorage.getItem(reloadKey)) < 10_000) return false
    sessionStorage.setItem(reloadKey, String(Date.now()))
  } catch {
    return false
  }
  location.reload()
  return true
}

interface Props {
  /** 变了就清掉错误：换到别的页面时不再显示上一个页面的报错 */
  resetKey: string
  /** 整页显示（顶栏也挂了，或者在登录页）；否则只占主内容区，顶栏和底部导航照常可用 */
  full: boolean
  /** 查询成功次数（见 useDataRev）：变了说明有新数据，值得再试一次渲染 */
  dataRev: number
  children: ReactNode
}

/**
 * 数据驱动的崩溃多是临时的（比如任务启动窗口里的脏数据），新数据到来就再试一次渲染，
 * 好了就自己恢复，不用手动刷新。上限兜底：真是代码 bug 时不能每个轮询周期都崩一遍。
 * 主内容区崩溃后顶栏的查询照常轮询，重试信号不会断；顶栏自己挂了（整页兜底那层）时
 * 轮询可能跟着停，自动重试未必触发，靠手动重新加载兜底。
 */
const maxAutoRetries = 20

class ErrorBoundary extends Component<Props, { error: unknown; reloading: boolean; retries: number }> {
  state = { error: null as unknown, reloading: false, retries: 0 }

  static getDerivedStateFromError(error: unknown) {
    return { error, reloading: false }
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error(error, info.componentStack)
    if (isChunkError(error) && reloadOnce()) this.setState({ reloading: true })
  }

  componentDidUpdate(prev: Props) {
    const { error, reloading, retries } = this.state
    if (prev.resetKey !== this.props.resetKey) {
      if (error || retries) this.setState({ error: null, reloading: false, retries: 0 })
      return
    }
    if (prev.dataRev === this.props.dataRev) return
    if (!error) {
      // 上次自动重试后渲染正常，额度恢复
      if (retries) this.setState({ retries: 0 })
    } else if (!reloading && !isChunkError(error) && retries < maxAutoRetries) {
      // 页面文件加载失败不参与自动重试：React lazy 缓存了失败的 import，重试渲染不会重新取文件，
      // 恢复靠上面的"自动刷新一次"和手动重新加载
      this.setState({ error: null, reloading: false, retries: retries + 1 })
    }
  }

  render() {
    const { error, reloading } = this.state
    if (!error) return this.props.children
    if (reloading) {
      return (
        <div className={cx('grid place-items-center', this.props.full ? 'min-h-dvh' : 'min-h-64')}>
          <Spinner />
        </div>
      )
    }
    return <ErrorPanel error={error} full={this.props.full} home={this.props.resetKey !== '/'} />
  }
}

function ErrorPanel({ error, full, home }: { error: unknown; full: boolean; home: boolean }) {
  const chunk = isChunkError(error)
  const message = error instanceof Error ? error.message : String(error)
  const panel = (
    <Card>
      <div role="alert" className="flex flex-col items-center gap-3 px-4 py-8 text-center">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-fg">{chunk ? '页面没能加载' : '这个页面出错了'}</h2>
          <p className="mx-auto max-w-prose text-ui leading-relaxed text-fg-3">
            {chunk
              ? '页面文件读取失败：丹炉可能刚升级、服务正在重启，或者网络断了。重新加载会取到最新的页面。'
              : '有新数据时会自动重试，也可以手动重新加载，任务不受影响。反复出现时，把下面这行报错记下来。'}
          </p>
        </div>
        {!chunk && <p className="max-w-full font-mono text-xs text-fg-2 [overflow-wrap:anywhere]">{message}</p>}
        <div className="flex flex-wrap justify-center gap-2">
          <Button size="sm" variant="primary" onClick={() => location.reload()}>
            重新加载
          </Button>
          {home && (
            <Link to="/" className={buttonClass('secondary', 'sm')}>
              回到总览
            </Link>
          )}
        </div>
      </div>
    </Card>
  )
  return full ? <div className="grid min-h-dvh place-items-center px-4 py-10"><div className="w-full max-w-[420px]">{panel}</div></div> : panel
}

/**
 * 页面出错时只把出错的部分换成说明和"重新加载"，不让整个面板变成白屏。
 * Layout 里包着主内容区（顶栏和底部导航还能用），main.tsx 里再包一层整页的，兜住顶栏和登录页。
 * 出错后查询每拿到一次新数据就自动重试一次渲染，临时性的数据错误不用手动刷新。
 */
export function PageBoundary({ full = false, children }: { full?: boolean; children: ReactNode }) {
  const { pathname } = useLocation()
  const dataRev = useDataRev()
  return (
    <ErrorBoundary resetKey={pathname} full={full} dataRev={dataRev}>
      {children}
    </ErrorBoundary>
  )
}

/** 查询每成功一次就 +1，作为"数据可能已经变了，值得再试一次渲染"的信号 */
function useDataRev() {
  const qc = useQueryClient()
  const [rev, setRev] = useState(0)
  useEffect(
    () =>
      qc.getQueryCache().subscribe((e) => {
        if (e.type === 'updated' && e.action.type === 'success') setRev((r) => r + 1)
      }),
    [qc],
  )
  return rev
}
