import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { rewriteDocLinks } from "./rewrite-doc-links.mjs";

const REPO = fileURLToPath(new URL("../../../", import.meta.url));
const transform = rewriteDocLinks({
  base: "/gopi",
  repo: "https://github.com/sonnes/gopi",
});

function rewrite(from, url) {
  const tree = { type: "root", children: [{ type: "link", url, children: [] }] };
  transform(tree, { path: `${REPO}${from}` });
  return tree.children[0].url;
}

describe("rewriteDocLinks", () => {
  it("routes a page link from a page", () => {
    expect(rewrite("web/src/content/docs/host/index.mdx", "../agent/index.mdx#run")).toBe(
      "/gopi/docs/agent/#run",
    );
  });

  it("routes a concept link from a page", () => {
    expect(
      rewrite(
        "web/src/content/docs/host/index.mdx",
        "../../../../../docs/concepts/host/host.md",
      ),
    ).toBe("/gopi/docs/concepts/host/host/");
  });

  it("routes a concept link and the concept index from a concept page", () => {
    const from = "docs/concepts/agent/loop.md";
    expect(rewrite(from, "../session/log.md")).toBe("/gopi/docs/concepts/session/log/");
    expect(rewrite(from, "../README.md")).toBe("/gopi/docs/concepts/");
  });

  it("links a file that the site does not show to GitHub", () => {
    const from = "docs/concepts/agent/loop.md";
    expect(rewrite(from, "../../README.md")).toBe(
      "https://github.com/sonnes/gopi/blob/main/docs/README.md",
    );
    expect(rewrite(from, "../../../pkg/agent/loop.go")).toBe(
      "https://github.com/sonnes/gopi/blob/main/pkg/agent/loop.go",
    );
  });
});
