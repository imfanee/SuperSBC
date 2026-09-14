import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: process.env.SBC_API_URL ?? "http://localhost:8080", changeOrigin: true },
    },
  },
  build: { sourcemap: false, chunkSizeWarningLimit: 1200 },
});
