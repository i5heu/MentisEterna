import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
    plugins: [vue()],
    build: {
        outDir: "../FrontEndDist",
        emptyOutDir: true,
    },
    server: {
        proxy: {
            "/login": "http://localhost:8080",
            "/health": "http://localhost:8080",
            "/notes": "http://localhost:8080",
            "/note-types": "http://localhost:8080",
            "/webauthn": "http://localhost:8080",
            "/tags": "http://localhost:8080",
            "/files": "http://localhost:8080",
            "/file": "http://localhost:8080",
            "/jobs": "http://localhost:8080",
            "/backup": "http://localhost:8080",
        },
    },
});
