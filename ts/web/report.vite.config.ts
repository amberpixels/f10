import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const here = (p: string) => fileURLToPath(new URL(p, import.meta.url));

// The eval report page: one script and one stylesheet that the eval runner
// inlines into each run's index.html. A module chunk loaded from file:// is
// blocked by the browser, so this is a library build, not a second page of
// the app.
export default defineConfig({
  root: here("."),
  publicDir: false,
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: here("../../cli/cmd/f10eval/page"),
    emptyOutDir: true,
    target: "es2022",
    sourcemap: false,
    reportCompressedSize: false,
    cssCodeSplit: false,
    lib: {
      entry: here("src/report/main.tsx"),
      formats: ["iife"],
      name: "f10page",
      fileName: () => "page.js",
      cssFileName: "page",
    },
  },
});
