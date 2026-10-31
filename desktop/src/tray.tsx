// 托盘旁的迷你总览（独立的小窗口）
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { TrayApp } from './TrayApp'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={new QueryClient()}>
      <TrayApp />
    </QueryClientProvider>
  </StrictMode>,
)
