import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// Tauri 开发时从固定端口加载界面（见 src-tauri/tauri.conf.json 的 devUrl）。
// 两个页面：主窗口 index.html，托盘旁的迷你总览 tray.html
export default defineConfig({
  plugins: [react(), tailwindcss()],
  clearScreen: false,
  server: { port: 17610, strictPort: true },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    rollupOptions: { input: { main: 'index.html', tray: 'tray.html' } },
  },
})
