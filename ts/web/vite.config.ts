import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const here = (p: string) => fileURLToPath(new URL(p, import.meta.url));

// The build lands in the Go tree, which embeds it into the daemon.
export default defineConfig({
  root: here("."),
  publicDir: here("public"),
  build: {
    outDir: here("../../cli/internal/webui/dist"),
    emptyOutDir: true,
    target: "es2022",
    sourcemap: false,
    reportCompressedSize: false,
  },
  test: {
    environment: "happy-dom",
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
