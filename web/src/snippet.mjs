// A snippet is the lines of a Go file between the comment
// "// snippet:<name>" and its "// end". The landing page imports a file
// of examples/landing with ?raw and shows one snippet of it, so the Go
// toolchain builds each line that the page shows.
//
// A snippet can hold another snippet. The markers of the inner snippet
// are not part of the outer one. A snippet drops each `if err != nil {`
// block, so a preview shows the calls that make the point. The common
// indent of the lines is removed.

const START = /^\s*\/\/ snippet:(\S+)\s*$/;
const END = /^\s*\/\/ end\s*$/;
const ERR = /^(\s*)if err != nil \{\s*$/;

export function snippet(source, name) {
  const lines = source.split("\n");
  const start = lines.findIndex((line) => line.match(START)?.[1] === name);
  if (start < 0) {
    throw new Error(`no snippet "${name}"`);
  }

  const body = [];
  let depth = 1;
  for (const line of lines.slice(start + 1)) {
    if (START.test(line)) {
      depth++;
      continue;
    }
    if (END.test(line)) {
      depth--;
      if (depth === 0) {
        return dedent(squeeze(dropErrors(body)));
      }
      continue;
    }
    body.push(line);
  }
  throw new Error(`snippet "${name}" has no end`);
}

// dropErrors removes each `if err != nil {` block, from its line to the
// brace at the same indent.
function dropErrors(lines) {
  const out = [];
  let close = null;
  for (const line of lines) {
    if (close !== null) {
      if (line === close) {
        close = null;
      }
      continue;
    }
    const m = line.match(ERR);
    if (m) {
      close = `${m[1]}}`;
      continue;
    }
    out.push(line);
  }
  return out;
}

// squeeze joins each run of blank lines into one, and removes the blank
// lines at the start and at the end.
function squeeze(lines) {
  const out = lines.filter(
    (line, i) => line.trim() !== "" || (i > 0 && lines[i - 1].trim() !== ""),
  );
  while (out.length > 0 && out[0].trim() === "") out.shift();
  while (out.length > 0 && out.at(-1).trim() === "") out.pop();
  return out;
}

function dedent(lines) {
  const indents = lines
    .filter((line) => line.trim() !== "")
    .map((line) => line.match(/^\s*/)[0].length);
  const cut = Math.min(...indents);
  return lines.map((line) => line.slice(cut)).join("\n");
}
