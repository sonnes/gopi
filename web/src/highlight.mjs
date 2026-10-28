// The highlighting of the landing page, in the style of xdb.dev: code is
// ink, a comment is grey, and a string is rose. Nothing else takes a
// colour, so the reader sees the calls and the values that they pass.
//
// highlight returns HTML. A comment gets the class "cm", a string gets
// the class "st", and every other character is escaped text.

const TOKEN = /(\/\/.*)|("(?:[^"\\]|\\.)*"|`[^`]*`)/g;

const ESCAPES = { "&": "&amp;", "<": "&lt;", ">": "&gt;" };

function escape(text) {
  return text.replace(/[&<>]/g, (c) => ESCAPES[c]);
}

export function highlight(code) {
  let html = "";
  let last = 0;
  for (const m of code.matchAll(TOKEN)) {
    html += escape(code.slice(last, m.index));
    const cls = m[1] ? "cm" : "st";
    html += `<span class="${cls}">${escape(m[0])}</span>`;
    last = m.index + m[0].length;
  }
  return html + escape(code.slice(last));
}
