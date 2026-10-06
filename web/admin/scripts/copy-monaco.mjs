// Copies Monaco's prebuilt editor into public/monaco/<version>/vs, where the
// editor page loads it at runtime with Monaco's own AMD loader. Loaded that
// way it stays out of the app's bundle (and the bundler's way), loads only on
// the editor page, and its language workers load only when a file needs one.
// The version in the path lets the gateway mark it cacheable for good.
import { cpSync, existsSync, readFileSync, rmSync, mkdirSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const pkgDir = join(here, "..", "node_modules", "monaco-editor");
const { version } = JSON.parse(readFileSync(join(pkgDir, "package.json"), "utf8"));
const outRoot = join(here, "..", "public", "monaco");
const out = join(outRoot, version, "vs");
if (existsSync(join(out, "loader.js"))) process.exit(0);
// Old versions go: the path is the cache key, and stale copies only weigh.
if (existsSync(outRoot)) for (const d of readdirSync(outRoot)) if (d !== version) rmSync(join(outRoot, d), { recursive: true, force: true });
mkdirSync(dirname(out), { recursive: true });
cpSync(join(pkgDir, "min", "vs"), out, { recursive: true });
console.log(`monaco ${version} → public/monaco/${version}/vs`);
