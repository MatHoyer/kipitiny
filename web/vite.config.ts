import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": path.resolve(import.meta.dirname, "./src") },
  },
  server: {
    // Keep the browser's Host: the API refuses mutations whose Origin differs.
    proxy: { "/api": { target: "http://localhost:8080", changeOrigin: false, ws: true } },
  },
});
