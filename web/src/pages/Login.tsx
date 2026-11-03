import { useId, useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { Brand } from '../components/Layout'
import { Button, Card, ErrorBox, Field, Input } from '../components/ui'
import { api } from '../lib/api'
import { ApiError } from '@danlu/core'

export function Login() {
  const nav = useNavigate()
  // 会话过期被踢回来时带着 next，登录后回到原来的页面；只接受站内路径
  const next = useSearchParams()[0].get('next')
  const dest = next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/login') ? next : '/'
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [needTotp, setNeedTotp] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(false)
  // 字段错误写在字段下方，不用浏览器自带的必填气泡
  const [errs, setErrs] = useState<Partial<Record<'username' | 'password' | 'code', string>>>({})
  const uid = useId()
  const ids = { username: `${uid}-username`, password: `${uid}-password`, code: `${uid}-code` }
  const field = (k: keyof typeof ids) => ({ id: ids[k], 'aria-invalid': !!errs[k] || undefined, 'aria-describedby': errs[k] ? `${ids[k]}-err` : undefined })
  const clear = (k: keyof typeof ids) => errs[k] && setErrs((e) => ({ ...e, [k]: undefined }))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const next: typeof errs = {}
    if (!username.trim()) next.username = '请填写用户名'
    if (!password) next.password = '请填写密码'
    if (needTotp && !/^\d{6}$/.test(code)) next.code = '请输入手机验证器 App 里的 6 位数字'
    setErrs(next)
    const first = (['username', 'password', 'code'] as const).find((k) => next[k])
    if (first) {
      document.getElementById(ids[first])?.focus()
      return
    }
    setLoading(true)
    setError(null)
    try {
      await api.login(username, password, needTotp ? code : undefined)
      nav(dest, { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.needTotp) {
        setNeedTotp(true)
        setCode('')
      }
      setError(err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="grid min-h-dvh place-items-center px-4 py-10">
      <div className="w-full max-w-[360px]">
        <div className="mb-6 flex flex-col items-center gap-3 text-center">
          <Brand className="size-10 rounded-lg [&_svg]:size-5" />
          <div>
            <h1 className="text-lg font-semibold">登录丹炉</h1>
            <p className="mt-0.5 text-ui text-fg-3">GPU 训练任务管理</p>
          </div>
        </div>
        <Card className="p-5">
          <form onSubmit={submit} noValidate className="space-y-4">
            <Field label="用户名" error={errs.username} errorId={`${ids.username}-err`}>
              <Input {...field('username')} value={username} onChange={(e) => (setUsername(e.target.value), clear('username'))} autoComplete="username" autoCapitalize="off" />
            </Field>
            <Field label="密码" error={errs.password} errorId={`${ids.password}-err`}>
              <Input {...field('password')} type="password" value={password} onChange={(e) => (setPassword(e.target.value), clear('password'))} autoComplete="current-password" autoFocus />
            </Field>
            {needTotp && (
              <Field label="两步验证码" error={errs.code} errorId={`${ids.code}-err`}>
                <Input
                  {...field('code')}
                  value={code}
                  onChange={(e) => (setCode(e.target.value.replace(/\D/g, '').slice(0, 6)), clear('code'))}
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  maxLength={6}
                  autoFocus
                />
              </Field>
            )}
            <ErrorBox error={error} />
            <Button type="submit" variant="primary" className="w-full" loading={loading}>
              登录
            </Button>
          </form>
        </Card>
        <p className="mt-4 text-center text-xs text-fg-3">建议把带安全入口的完整地址存成书签</p>
      </div>
    </div>
  )
}
