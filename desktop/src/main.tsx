import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { useLocale } from '@danlu/core'
import { App } from './App'
import { ToastProvider } from './components/ui'
import './index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    // 汇总数据由 Rust 层推送，这里的查询只是按需取的详情，失败了由界面显示
    queries: { retry: false, refetchOnWindowFocus: false },
  },
})

/** 语言切换时以 key={locale} 重挂整棵树，所有组件拿到新文案；查询缓存挂在模块级，不受重挂影响 */
function Root() {
  const locale = useLocale()
  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])
  return (
    <QueryClientProvider client={queryClient} key={locale}>
      <ToastProvider>
        <App />
      </ToastProvider>
    </QueryClientProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)