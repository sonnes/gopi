// Screenshot a docs page in light or dark with headless Chrome over CDP.
// Usage: node shot.mjs <url> <out.png> [light|dark] [width] [selector]
// With a selector, the shot is clipped to the union of every match plus 16px.
import { spawn } from "node:child_process";
import { writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [url, out, theme = "light", width = "1280", selector] = process.argv.slice(2);
const CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const port = 9300 + Math.floor(Math.random() * 600);
const profile = mkdtempSync(join(tmpdir(), "shot-"));
const chrome = spawn(CHROME, [
  "--headless=new", "--disable-gpu", "--hide-scrollbars",
  `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`,
  `--window-size=${width},1000`, "about:blank",
], { stdio: "ignore" });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let targets;
for (let i = 0; i < 50; i++) {
  try { targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json(); break; } catch { await sleep(100); }
}
const page = targets.find((t) => t.type === "page");
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r));
let id = 0; const waiting = new Map();
ws.addEventListener("message", (e) => {
  const m = JSON.parse(e.data);
  if (m.id && waiting.has(m.id)) { waiting.get(m.id)(m); waiting.delete(m.id); }
});
const send = (method, params = {}) => new Promise((r) => { const i = ++id; waiting.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });

await send("Page.enable");
await send("Runtime.enable");
await send("Page.addScriptToEvaluateOnNewDocument", {
  source: `try{localStorage.setItem('starlight-theme','${theme}')}catch(e){}`,
});
await send("Page.navigate", { url });
await sleep(3500);
const evalv = async (expr) => (await send("Runtime.evaluate", { expression: expr, returnByValue: true })).result.result.value;
await evalv(`document.documentElement.dataset.theme='${theme}'`);
let clip;
if (selector) {
  clip = await evalv(`(() => { const els=[...document.querySelectorAll(${JSON.stringify(selector)})]; if(!els.length) return null;
    let x1=1e9,y1=1e9,x2=0,y2=0; for (const el of els){const r=el.getBoundingClientRect(); x1=Math.min(x1,r.left); y1=Math.min(y1,r.top+scrollY); x2=Math.max(x2,r.right); y2=Math.max(y2,r.bottom+scrollY);}
    return {x:Math.max(0,x1-16), y:Math.max(0,y1-16), width:x2-x1+32, height:y2-y1+32, scale:1}; })()`);
  if (!clip) { console.error("no element matches", selector); }
}
const h = await evalv("document.documentElement.scrollHeight");
const shot = await send("Page.captureScreenshot", {
  format: "png", captureBeyondViewport: true,
  clip: clip ?? { x: 0, y: 0, width: Number(width), height: h, scale: 1 },
});
writeFileSync(out, Buffer.from(shot.result.data, "base64"));
console.log(out);
ws.close(); chrome.kill();
process.exit(0);
