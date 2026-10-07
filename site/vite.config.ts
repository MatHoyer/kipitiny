import { readFileSync } from "node:fs";
import { reactRouter } from "@react-router/dev/vite";
import { defineConfig } from "vite";

/** The kipitiny version the docs describe, shown in the footer: the repo's VERSION file (KIPITINY_VERSION in the Docker build). */
function version() {
  if (process.env.KIPITINY_VERSION) return process.env.KIPITINY_VERSION;
  try {
    return readFileSync("../VERSION", "utf8").trim();
  } catch {
    return "";
  }
}

export default defineConfig({
  plugins: [reactRouter()],
  resolve: { tsconfigPaths: true },
  define: { __KIPITINY_VERSION__: JSON.stringify(version()) },
});
