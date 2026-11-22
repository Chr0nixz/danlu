// 托盘旁的迷你总览（独立的小窗口）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { useLocale } from '@danlu/core'
import './index.css'
import { TrayApp } from './TrayApp'

const queryClient = new QueryClient()

/** 语言切换时以 key={locale} 重挂整棵树，和主窗口同一套机制 */
function Root() {
  const locale = useLocale()
  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])
  return (
    <QueryClientProvider client={queryClient} key={locale}>
      <TrayApp />
    </QueryClientProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)