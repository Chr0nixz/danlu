import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // 构建产物直接放进 Go 的 embed 目录
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1000,
  },
  server: {
    // 本地开发：先用 tls=off 启动 danlu（端口 17600），浏览器访问一次安全入口拿到 cookie
    proxy: { '/api': 'http://127.0.0.1:17600' },
  },
})
