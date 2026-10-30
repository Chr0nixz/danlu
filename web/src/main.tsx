import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'
import { Layout } from './components/Layout'
import './index.css'
import { ApiError } from './lib/api'
import { Home } from './pages/Home'
import { JobDetail } from './pages/JobDetail'
import { Jobs } from './pages/Jobs'
import { Login } from './pages/Login'
import { NewJob } from './pages/NewJob'
import { NotFound } from './pages/NotFound'
import { Settings } from './pages/Settings'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // 401/404 重试没有意义
      retry: (n, err) => n < 2 && !(err instanceof ApiError && err.status < 500),
      refetchOnWindowFocus: true,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route element={<Layout />}>
            <Route path="/" element={<Home />} />
            <Route path="/jobs" element={<Jobs />} />
            <Route path="/jobs/:id" element={<JobDetail />} />
            <Route path="/new" element={<NewJob />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
