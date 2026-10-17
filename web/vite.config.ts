import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { resolve } from 'node:path'

// Multi-page build: the drive app, the server window (admin) and the public
// link page. The Go server embeds the dist folder.
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'index.html'),
        admin: resolve(__dirname, 'admin.html'),
        share: resolve(__dirname, 'share.html'),
        client: resolve(__dirname, 'client.html'),
      },
    },
  },
  server: {
    // `npm run dev` talks to a locally running homelydrive-server.
    proxy: {
      '/api': 'http://127.0.0.1:8420',
      '/dav': 'http://127.0.0.1:8420',
    },
  },
})
