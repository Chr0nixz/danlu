import { ApiError, createApi, observeServerDate, type Transport } from '@danlu/core'
import { t } from './i18n'

// 面板和服务器同源：直接 fetch，靠 gate cookie 和会话 cookie 认证
const transport: Transport = {
  async request(method, path, body) {
    let res: Response
    let text: string
    try {
      const sent = Date.now()
      res = await fetch(path, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body,
      })
      // 本机时钟不准时，等待时长、倒计时都会偏：按响应头 Date 校正 now()
      observeServerDate(res.headers.get('Date'), sent, Date.now())
      text = await res.text()
    } catch {
      // 没连上服务器：status 0，和桌面端同一套约定；文案直白，别把浏览器的英文 TypeError 抛给用户
      throw new ApiError(0, t('error.network'))
    }
    if (res.status === 401 && !location.pathname.startsWith('/login')) {
      // 登录后回到原来的页面
      location.href = `/login?next=${encodeURIComponent(location.pathname + location.search)}`
    }
    return { status: res.status, body: text }
  },
  stream(path, h) {
    const es = new EventSource(path)
    es.onmessage = (ev) => h.onEvent('message', ev.data)
    es.addEventListener('end', (ev) => h.onEvent('end', (ev as MessageEvent).data))
    // 浏览器的自动重连会带着旧的 offset，所以出错就关掉，由调用方自己重连
    es.onerror = () => {
      es.close()
      h.onError()
    }
    return () => es.close()
  },
}

export const api = createApi(transport, {
  // 安全入口 cookie 过期后，所有请求都会是空的 404
  emptyNotFound: () => t('error.gate_expired'),
})
