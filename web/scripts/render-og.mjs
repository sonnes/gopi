// Renders the cover of the site, web/design/og/og.html, to a 1200 by 630
// PNG with headless Chrome over CDP. The site uses the PNG as its social
// card, and the README shows it as its banner.
//
// Usage: node web/scripts/render-og.mjs <og.html> <out.png>
import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const [src, out] = process.argv.slice(2);
if (!src || !out) {
  console.error("usage: render-og.mjs <og.html> <out.png>");
  process.exit(2);
}

const WIDTH = 1200;
const HEIGHT = 630;
// The fonts load from Google Fonts. The render waits this long for them.
const SETTLE_MS = 3000;
const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";

const port = 9300 + Math.floor(Math.random() * 600);
const profile = mkdtempSync(join(tmpdir(), "og-"));
const chrome = spawn(
  CHROME,
  [
    "--headless=new",
    "--disable-gpu",
    "--hide-scrollbars",
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`,
    `--window-size=${WIDTH},${HEIGHT}`,
    "about:blank",
  ],
  { stdio: "ignore" },
);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let targets;
for (let i = 0; i < 50 && !targets; i++) {
  try {
    targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
  } catch {
    await sleep(100);
  }
}
const page = targets.find((t) => t.type === "page");
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r));

let id = 0;
const waiting = new Map();
ws.addEventListener("message", (e) => {
  const m = JSON.parse(e.data);
  if (m.id && waiting.has(m.id)) {
    waiting.get(m.id)(m);
    waiting.delete(m.id);
  }
});
const send = (method, params = {}) =>
  new Promise((r) => {
    const i = ++id;
    waiting.set(i, r);
    ws.send(JSON.stringify({ id: i, method, params }));
  });

await send("Page.enable");
await send("Emulation.setDeviceMetricsOverride", {
  width: WIDTH,
  height: HEIGHT,
  deviceScaleFactor: 1,
  mobile: false,
});
await send("Page.navigate", { url: `file://${resolve(src)}` });
await sleep(SETTLE_MS);

const shot = await send("Page.captureScreenshot", {
  format: "png",
  clip: { x: 0, y: 0, width: WIDTH, height: HEIGHT, scale: 1 },
});
writeFileSync(out, Buffer.from(shot.result.data, "base64"));
console.log(`render-og: wrote ${out}`);
ws.close();
chrome.kill();
process.exit(0);
