import { createRequire } from "node:module";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { openSync } from "fontkit";
import { describe, expect, it } from "vitest";

import { failOnMermaidErrors, mermaidErrors, renderMermaid } from "./render-mermaid.mjs";

const require = createRequire(import.meta.url);
const REPO = fileURLToPath(new URL("../../../", import.meta.url));

const FONTS = {
  regular: "@fontsource/noto-sans/files/noto-sans-latin-400-normal.woff",
  bold: "@fontsource/noto-sans/files/noto-sans-latin-700-normal.woff",
  mono: "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-400-normal.woff",
  monoBold: "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-700-normal.woff",
};

function treeWith(value) {
  return { type: "root", children: [{ type: "code", lang: "mermaid", value }] };
}

describe("renderMermaid", () => {
  const transform = renderMermaid({ fonts: FONTS });

  it("renders a Mermaid block of a concept page to an SVG", () => {
    const tree = treeWith("flowchart LR\n  A --> B");
    transform(tree, { path: `${REPO}docs/concepts/ai/stream.md` });
    const [node] = tree.children;
    expect(node.type).toBe("html");
    expect(node.value).toMatch(/^<figure class="fig not-content pele-fig">/);
    expect(node.value).toContain('class="pele pele-flowchart"');
    expect(node.value).toContain("var(--pele-fg");
  });

  it("stops the build on a Mermaid block in a page", () => {
    const tree = treeWith("flowchart LR\n  A --> B");
    expect(() =>
      transform(tree, { path: `${REPO}web/src/content/docs/host/index.mdx` }),
    ).toThrow(/belongs in a concept page/);
  });

  it("stops the build on a Mermaid block that does not parse", () => {
    const dir = mkdtempSync(join(tmpdir(), "mermaid-"));
    writeFileSync(join(dir, "bad.md"), "# Bad\n\n```mermaid\nflowchart LR\n  call --> B\n```\n");
    expect(mermaidErrors(dir)).toEqual([expect.stringMatching(/^bad\.md: Parse error/)]);
    const start = failOnMermaidErrors({ dir }).hooks["astro:build:start"];
    expect(() => start()).toThrow(/bad\.md/);
  });

  it("parses each Mermaid block of the concept pages", () => {
    expect(mermaidErrors(`${REPO}docs/concepts`)).toEqual([]);
  });

  it("keeps a label inside its box in the site font", () => {
    const tree = treeWith("flowchart LR\n  A[EventApprovalStart]");
    transform(tree, { path: `${REPO}docs/concepts/ai/stream.md` });
    const box = tree.children[0].value.match(
      /<g class="pele-node[^"]*" data-id="A"[^>]*><rect [^>]*width="([\d.]+)"/,
    );
    expect(box).not.toBeNull();
    // The width of the label in Noto Sans at 16px.
    const font = openSync(require.resolve(FONTS.regular));
    const label = font.layout("EventApprovalStart").advanceWidth / font.unitsPerEm;
    expect(Number(box[1])).toBeGreaterThan(label * 16);
  });
});
