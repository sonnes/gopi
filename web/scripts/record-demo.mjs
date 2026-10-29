// Records the events of the landing page. It runs the demo program of
// examples/landing/demo twice on one sessions folder. The first run gets
// a task, and the script kills it during its third tool call. The second
// run gets no task and continues the session. The script writes the
// agent events of the second run to a JSON file that
// src/pages/index.astro reads.
//
// Usage: node web/scripts/record-demo.mjs <demo binary> <out.json>
//
// If a run gives an output that the page does not expect, the script
// stops and writes nothing.
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [bin, out] = process.argv.slice(2);
if (!bin || !out) {
  console.error("usage: record-demo.mjs <demo binary> <out.json>");
  process.exit(2);
}

const TASK = "Triage the open issues";
// The first run stops when it starts this many tool calls.
const KILL_AFTER_CALLS = 3;
const ANSWER = "Two issues are open: #41 is a bug, #42 asks for a feature.";

function run(args, { killAfterCalls } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(bin, args);
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      const calls = stdout.split("\n").filter((l) => l.startsWith("->")).length;
      if (killAfterCalls && calls >= killAfterCalls) {
        child.kill("SIGKILL");
      }
    });
    child.stderr.on("data", (chunk) => (stderr += chunk));
    child.on("error", reject);
    child.on("close", (code, signal) => {
      const lines = stdout.split("\n").filter((l) => l !== "");
      resolve({ code, signal, lines, stderr });
    });
  });
}

function fail(message) {
  console.error(`record-demo: ${message}`);
  process.exit(1);
}

const dir = mkdtempSync(join(tmpdir(), "gopi-demo-"));
try {
  const first = await run(["-dir", dir, TASK], { killAfterCalls: KILL_AFTER_CALLS });
  if (first.signal !== "SIGKILL") {
    fail(`the first run ended with ${first.code ?? first.signal}, not a kill`);
  }

  const second = await run(["-dir", dir, "-events"]);
  if (second.code !== 0) {
    fail(`the second run exited with ${second.code}: ${second.stderr}`);
  }
  if (second.lines.at(-1) !== ANSWER) {
    fail(`the second run ended with "${second.lines.at(-1)}"`);
  }

  // Each event keeps its type, and the name of its tool if it has one.
  const events = second.stderr
    .split("\n")
    .filter((l) => l.startsWith("{"))
    .map((l) => JSON.parse(l))
    .map((e) => {
      const kind = e.assistant_event?.type;
      return {
        type: e.type,
        ...(e.tool_name && { tool: e.tool_name }),
        ...(kind && { delta: kind }),
        ...(e.message?.role && { role: e.message.role }),
      };
    });

  writeFileSync(out, `${JSON.stringify({ events }, null, 2)}\n`);
  console.log(`record-demo: wrote ${out}`);
} finally {
  rmSync(dir, { recursive: true, force: true });
}
