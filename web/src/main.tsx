import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode, Suspense, lazy, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'
import { PageBoundary } from './components/ErrorBoundary'
import { Layout } from './components/Layout'
import './index.css'
import { ApiError, useLocale } from '@danlu/core'
import { Home } from './pages/Home'
import { Login } from './pages/Login'
import { Spinner } from './components/ui'
import { t } from './lib/i18n'

const Jobs = lazy(() => import('./pages/Jobs').then((m) => ({ default: m.Jobs })))
const JobDetail = lazy(() => import('./pages/JobDetail').then((m) => ({ default: m.JobDetail })))
const NewJob = lazy(() => import('./pages/NewJob').then((m) => ({ default: m.NewJob })))
const Settings = lazy(() => import('./pages/settings/SettingsLayout').then((m) => ({ default: m.SettingsLayout })))
const NotFound = lazy(() => import('./pages/NotFound').then((m) => ({ default: m.NotFound })))
const Terminal = lazy(() => import('./pages/Terminal').then((m) => ({ default: m.Terminal })))
const Compare = lazy(() => import('./pages/Compare').then((m) => ({ default: m.Compare })))
const Resources = lazy(() => import('./pages/Resources').then((m) => ({ default: m.Resources })))
const pageFallback = <div className="grid min-h-64 place-items-center"><Spinner /></div>

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // 只有 4xx（401、404 之类）重试没有意义。断网和服务重启时 status 是 0，要重试，
      // 不然一次网络抖动就把报错交给页面
      retry: (n, err) => n < 2 && !(err instanceof ApiError && err.status >= 400 && err.status < 500),
      refetchOnWindowFocus: true,
    },
  },
})

/**
 * 语言切换时以 key={locale} 重挂整棵树：t 是纯函数，这样所有组件（含 memo 的）
 * 都拿到新语言的文案，不必逐个订阅。查询缓存挂在模块级 queryClient 上，不受重挂影响。
 */
function Root() {
  const locale = useLocale()
  useEffect(() => {
    document.documentElement.lang = locale
    document.title = t('layout.title')
  }, [locale])
  return (
    <QueryClientProvider client={queryClient} key={locale}>
      <BrowserRouter>
        {/* 整页兜底：顶栏或登录页出错时也不白屏。主内容区另有一层，见 Layout */}
        <PageBoundary full>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route element={<Layout />}>
              <Route path="/" element={<Home />} />
              <Route path="/jobs" element={<Suspense fallback={pageFallback}><Jobs /></Suspense>} />
              <Route path="/jobs/:id" element={<Suspense fallback={pageFallback}><JobDetail /></Suspense>} />
              <Route path="/compare" element={<Suspense fallback={pageFallback}><Compare /></Suspense>} />
              <Route path="/resources" element={<Suspense fallback={pageFallback}><Resources /></Suspense>} />
              <Route path="/new" element={<Suspense fallback={pageFallback}><NewJob /></Suspense>} />
              <Route path="/settings" element={<Suspense fallback={pageFallback}><Settings /></Suspense>} />
              <Route path="/settings/:group" element={<Suspense fallback={pageFallback}><Settings /></Suspense>} />
              <Route path="/terminal" element={<Suspense fallback={pageFallback}><Terminal /></Suspense>} />
              <Route path="*" element={<Suspense fallback={pageFallback}><NotFound /></Suspense>} />
            </Route>
          </Routes>
        </PageBoundary>
      </BrowserRouter>
    </QueryClientProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
