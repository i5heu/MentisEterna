import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../FrontEndDist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/login': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/notes': 'http://localhost:8080',
    },
  },
})
