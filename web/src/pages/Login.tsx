import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { Brand } from '../components/Layout'
import { Button, Card, ErrorBox, Field, Input } from '../components/ui'
import { api } from '../lib/api'

export function Login() {
  const nav = useNavigate()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError(null)
    try {
      await api.login(username, password)
      nav('/', { replace: true })
    } catch (err) {
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
