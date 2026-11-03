import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { StrictMode, Suspense, lazy } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'
import { Layout } from './components/Layout'
import './index.css'
import { ApiError } from '@danlu/core'
import { Home } from './pages/Home'
import { Login } from './pages/Login'
import { Spinner } from './components/ui'

const Jobs = lazy(() => import('./pages/Jobs').then((m) => ({ default: m.Jobs })))
const JobDetail = lazy(() => import('./pages/JobDetail').then((m) => ({ default: m.JobDetail })))
const NewJob = lazy(() => import('./pages/NewJob').then((m) => ({ default: m.NewJob })))
const Settings = lazy(() => import('./pages/Settings').then((m) => ({ default: m.Settings })))
const NotFound = lazy(() => import('./pages/NotFound').then((m) => ({ default: m.NotFound })))
const Terminal = lazy(() => import('./pages/Terminal').then((m) => ({ default: m.Terminal })))
const Compare = lazy(() => import('./pages/Compare').then((m) => ({ default: m.Compare })))
const pageFallback = <div className="grid min-h-64 place-items-center"><Spinner /></div>

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
            <Route path="/jobs" element={<Suspense fallback={pageFallback}><Jobs /></Suspense>} />
            <Route path="/jobs/:id" element={<Suspense fallback={pageFallback}><JobDetail /></Suspense>} />
            <Route path="/compare" element={<Suspense fallback={<div className="grid min-h-64 place-items-center"><Spinner /></div>}><Compare /></Suspense>} />
            <Route path="/new" element={<Suspense fallback={pageFallback}><NewJob /></Suspense>} />
            <Route path="/settings" element={<Suspense fallback={pageFallback}><Settings /></Suspense>} />
            <Route path="/terminal" element={<Suspense fallback={<div className="grid min-h-64 place-items-center"><Spinner /></div>}><Terminal /></Suspense>} />
            <Route path="*" element={<Suspense fallback={pageFallback}><NotFound /></Suspense>} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
