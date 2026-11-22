import { Check, Copy, X } from 'lucide-react'
import { useRef, useState } from 'react'
import { t } from './i18n'

/**
 * 复制到剪贴板，结果如实反馈：浏览器只在 HTTPS（或本机地址）下开放剪贴板，tls = off 时用 IP 打开面板就复制不了；
 * 开放了也可能被拒绝（例如页面没有焦点）。失败时不能照样显示对勾。结果也读给读屏听。
 */
export function useCopy(text: string) {
  const [state, setState] = useState<'idle' | 'ok' | 'fail'>('idle')
  const [why, setWhy] = useState('')
  const timer = useRef<number>(undefined)
  const copy = async () => {
    window.clearTimeout(timer.current)
    if (!navigator.clipboard) {
      setWhy(t('ui.copy_https_only'))
      setState('fail')
    } else {
      try {
        await navigator.clipboard.writeText(text)
        setState('ok')
      } catch {
        setWhy(t('ui.copy_denied'))
        setState('fail')
      }
    }
    timer.current = window.setTimeout(() => setState('idle'), 3000)
  }
  const icon = state === 'ok' ? <Check className="size-4" aria-hidden /> : state === 'fail' ? <X className="size-4" aria-hidden /> : <Copy className="size-4" aria-hidden />
  const status = (
    <span role="status" className="sr-only">
      {state === 'ok' ? t('common.copied') : state === 'fail' ? t('ui.copy_failed_sr', { why }) : ''}
    </span>
  )
  const title = state === 'fail' ? t('ui.copy_failed_title', { why }) : undefined
  return { state, copy, icon, status, title }
}
