import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.BACKEND_TARGET || 'http://localhost:8088',
        changeOrigin: true
      }
    }
  }
})
