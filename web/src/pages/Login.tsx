import { useState, type FormEvent } from 'react'
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

  const submit = async (e: FormEvent) => {
    e.preventDefault()
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
          <form onSubmit={submit} className="space-y-4">
            <Field label="用户名">
              <Input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" autoCapitalize="off" required />
            </Field>
            <Field label="密码">
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" autoFocus required />
            </Field>
            {needTotp && (
              <Field label="两步验证码">
                <Input
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  pattern="[0-9]{6}"
                  maxLength={6}
                  autoFocus
                  required
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
