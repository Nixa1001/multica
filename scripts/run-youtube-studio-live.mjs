#!/usr/bin/env node
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { createWriteStream, existsSync, mkdirSync } from "node:fs";
import { createConnection } from "node:net";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const bin = (name, fallback) => process.env[name] || fallback;
const node = bin("NODE_BIN", "/home/linuxbrew/.linuxbrew/bin/node");
const go = bin("GO_BIN", "/home/linuxbrew/.linuxbrew/bin/go");
const npm = bin("NPM_BIN", "/home/linuxbrew/.linuxbrew/bin/npm");
const pnpm = bin("PNPM_BIN", "/home/linuxbrew/.linuxbrew/bin/pnpm");
const pnpmArgs = existsSync(pnpm) ? [] : ["exec", "--yes", "--package=pnpm@10.28.2", "--", "pnpm"];
const localPlaywright = resolve(root, "node_modules/.bin/playwright");
const installedBrowser = "/home/nixa1001/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome";
const playwrightImage = process.env.PLAYWRIGHT_CONTAINER_IMAGE || "";
const port = process.env.LIVE_E2E_PORT || "18080";
const webPort = process.env.LIVE_E2E_WEB_PORT || "13000";
const dbPort = process.env.LIVE_E2E_DB_PORT || "15432";
const containerName = `multica-studio-e2e-${process.pid}`;
const logDir = resolve(root, ".artifacts/youtube-studio-live");
mkdirSync(logDir, { recursive: true });
const dbUrl = `postgres://multica:multica@127.0.0.1:${dbPort}/multica?sslmode=disable`;
const env = { ...process.env, PATH: `/home/linuxbrew/.linuxbrew/bin:${process.env.PATH ?? ""}`, DATABASE_URL: dbUrl, PORT: port, FRONTEND_PORT: webPort, CORS_ALLOWED_ORIGINS: `http://localhost:${webPort}`, FRONTEND_ORIGIN: `http://localhost:${webPort}`, NEXT_PUBLIC_API_URL: `http://localhost:${port}`, PLAYWRIGHT_BASE_URL: `http://localhost:${webPort}`, MULTICA_DEV_VERIFICATION_CODE: "428731", APP_ENV: "test" };
const children = [];
const run = (command, args, extra = {}) => {
  const log = resolve(logDir, `${extra.name ?? command.replaceAll("/", "_")}.log`);
  const out = createWriteStream(log, { flags: "w" });
  const child = spawn(command, args, { cwd: extra.cwd ?? root, env: { ...env, ...(extra.env ?? {}) }, detached: true, stdio: ["ignore", "pipe", "pipe"] });
  child.stdout.pipe(out);
  child.stderr.pipe(out);
  children.push(child);
  return child;
};
const waitPort = (host, value, timeout = 30000) => new Promise((resolvePromise, reject) => {
  const started = Date.now();
  const probe = () => { const socket = createConnection({ host, port: value }, () => { socket.destroy(); resolvePromise(); }); socket.on("error", () => { socket.destroy(); if (Date.now() - started > timeout) reject(new Error(`readiness timeout: ${host}:${value}`)); else setTimeout(probe, 250); }); };
  probe();
});
const waitHttp = async (url, timeout = 60000) => {
  const started = Date.now();
  let lastError = "no response";
  while (Date.now() - started <= timeout) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
      lastError = `HTTP ${response.status}`;
    } catch (error) { lastError = error instanceof Error ? error.message : String(error); }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 250));
  }
  throw new Error(`readiness timeout: ${url} (${lastError})`);
};
const waitHttpServer = async (url, timeout = 60000) => {
  const started = Date.now();
  let lastError = "no response";
  while (Date.now() - started <= timeout) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(2000) });
      if (response.status < 500) return;
      lastError = `HTTP ${response.status}`;
    } catch (error) { lastError = error instanceof Error ? error.message : String(error); }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 250));
  }
  throw new Error(`readiness timeout: ${url} (${lastError})`);
};
const stop = () => {
  for (const child of children) {
    if (!child.killed) {
      try { process.kill(-child.pid, "SIGTERM"); } catch { child.kill("SIGTERM"); }
    }
  }
};
process.on("SIGINT", stop); process.on("SIGTERM", stop);
try {
  if (!existsSync(pnpm) && !existsSync(npm)) throw new Error(`pnpm missing at ${pnpm} and npm missing at ${npm}`);
  const browserEnv = !playwrightImage && existsSync(installedBrowser) ? { ...env, PLAYWRIGHT_EXECUTABLE_PATH: installedBrowser } : env;
  if (!playwrightImage && !existsSync(installedBrowser)) {
    const playwrightInstall = spawnSync(localPlaywright, ["install", "chromium"], { cwd: root, env: { ...env, PATH: `/home/linuxbrew/.linuxbrew/bin:${process.env.PATH ?? ""}` }, stdio: "inherit" });
    if (playwrightInstall.status !== 0) throw new Error(`Playwright browser installation failed with exit code ${playwrightInstall.status}`);
  }
  run("/usr/bin/docker", ["run", "--rm", "--name", containerName, "-e", "POSTGRES_DB=multica", "-e", "POSTGRES_USER=multica", "-e", "POSTGRES_PASSWORD=multica", "-p", `127.0.0.1:${dbPort}:5432`, "pgvector/pgvector:pg17"], { name: "postgres" });
  await waitPort("127.0.0.1", dbPort, 60000);
  const migration = run(go, ["run", "./cmd/migrate", "up"], { name: "migrate", cwd: resolve(root, "server"), env: { DATABASE_URL: dbUrl } });
  const migrationCode = await new Promise((resolvePromise) => migration.on("exit", (value) => resolvePromise(value ?? 1)));
  if (migrationCode !== 0) throw new Error(`migration failed with exit code ${migrationCode}`);
  const server = run(go, ["run", "./cmd/server"], { name: "server", cwd: resolve(root, "server") });
  await waitHttp(`http://localhost:${port}/healthz`);
  const web = run(existsSync(pnpm) ? pnpm : npm, [...pnpmArgs, "--filter", "@multica/web", "dev"], { name: "web" });
  // Do not use GET / as readiness: a cold Next dev server compiles the entire
  // application route there and can exceed the probe window before Playwright
  // has a chance to start. Any non-5xx response from a static probe proves the
  // HTTP server is accepting connections; the real route remains Playwright's
  // responsibility and is covered by the authenticated browser assertions.
  await waitHttpServer(`http://localhost:${webPort}/favicon.ico`);
  const testCommand = playwrightImage ? "/usr/bin/docker" : localPlaywright;
  const testArgs = playwrightImage
    ? ["run", "--rm", "--network", "host", "--name", `multica-pri36-playwright-${process.pid}`, "-v", `${root}:/work`, "-w", "/work", ...Object.entries(browserEnv).filter(([key]) => ["DATABASE_URL", "PORT", "FRONTEND_PORT", "NEXT_PUBLIC_API_URL", "PLAYWRIGHT_BASE_URL", "MULTICA_DEV_VERIFICATION_CODE", "APP_ENV"].includes(key)).flatMap(([key, value]) => ["-e", `${key}=${value}`]), playwrightImage, "node_modules/.bin/playwright", "test", "e2e/youtube-studio-live.spec.ts", "--project=chromium"]
    : ["test", "e2e/youtube-studio-live.spec.ts", "--project=chromium"];
  const test = spawn(testCommand, testArgs, { cwd: root, env: { ...browserEnv, PATH: `/home/linuxbrew/.linuxbrew/bin:${process.env.PATH ?? ""}` }, stdio: "inherit" });
  const code = await new Promise((resolvePromise) => test.on("exit", (value) => resolvePromise(value ?? 1)));
  if (code !== 0) throw new Error(`Playwright failed with exit code ${code}`);
  void server; void web; void npm; console.log(`PASS: live Studio harness; logs: ${logDir}`);
} catch (error) {
  console.error(error instanceof Error ? error.message : error);
  console.error(`Inspect readiness logs in ${logDir}`);
  process.exitCode = 1;
} finally {
  stop();
  try { execFileSync("/usr/bin/docker", ["rm", "-f", containerName], { stdio: "ignore" }); } catch { /* container already exited */ }
}
