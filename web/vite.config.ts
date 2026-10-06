import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发：Vite（:5173）把 /api、/ws 代理到本机面板，默认 :8080；换端口时由 scripts/dev.sh 设置 VPSMON_DEV_API（设计 28.1）
const api = process.env.VPSMON_DEV_API ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': api,
      '/ws': { target: api.replace(/^http/, 'ws'), ws: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // ECharts（含 zrender，已按需引入）单独成块：页面代码变化时不必重新下载，带图表的页面共用同一份缓存。
    // 它本身约 450 KB（gzip 后约 150 KB），提高告警阈值，避免每次构建都提示
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        manualChunks: (id) => (/node_modules\/(echarts|zrender)\//.test(id) ? 'echarts' : undefined),
      },
    },
  },
})
