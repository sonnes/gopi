import { dirname, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * A page links to its neighbours and to the repository with a plain
 * relative path, the way any markdown file does:
 *
 *     [Tools](../howto/tools.mdx)                a sibling page
 *     [tool.go](../../../../pkg/ai/tool.go)      a source file
 *
 * This plugin rewrites them for the site, where neither resolves: a
 * page target becomes a base-aware route, and everything else inside
 * the repository becomes an absolute link into GitHub.
 *
 * An author therefore never writes a site route by hand, and
 * scripts/check-links.mjs can make sure that every target exists.
 */

const PAGES_DIR = fileURLToPath(new URL("../content/docs/", import.meta.url));
const REPO_DIR = fileURLToPath(new URL("../../../", import.meta.url));

const EXTERNAL = /^(?:[a-z][a-z0-9+.-]*:|\/\/|#)/i;
const DOC_EXT = /\.mdx?$/;

// Trailing "/index" is a directory index, and the collection maps the
// root index.mdx to the bare "docs" id (see src/content.config.ts).
function slugFor(absPath) {
  const rel = relative(PAGES_DIR, absPath).split(sep).join("/");
  const noExt = rel.replace(DOC_EXT, "");
  const noIndex = noExt.replace(/(^|\/)index$/, "");
  return noIndex.replace(/\/$/, "");
}

function isInside(dir, absPath) {
  const rel = relative(dir, absPath);
  return rel !== "" && !rel.startsWith("..") && !rel.startsWith(sep);
}

function walk(node, visit) {
  if (!node || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const child of node) walk(child, visit);
    return;
  }
  if (node.type === "link") visit(node);
  // definition nodes carry the url of a reference-style link
  if (node.type === "definition") visit(node);
  if (Array.isArray(node.children)) walk(node.children, visit);
}

/**
 * @param {object} options
 * @param {string} options.base   Astro's `base`, e.g. "/gopi".
 * @param {string} options.repo   Repository URL, no trailing slash.
 * @param {string} [options.branch]
 */
export function rewriteDocLinks({ base, repo, branch = "main" }) {
  const prefix = base === "/" ? "" : base.replace(/\/$/, "");

  return (tree, file) => {
    const from = file?.path ?? file?.history?.[0];
    if (!from) return;
    const fromDir = dirname(from);

    walk(tree, (node) => {
      const url = node.url;
      if (typeof url !== "string" || url === "" || EXTERNAL.test(url)) return;

      const [pathPart, ...rest] = url.split(/(?=[#?])/);
      const suffix = rest.join("");
      if (pathPart === "") return;

      const target = resolve(fromDir, pathPart);

      if (DOC_EXT.test(pathPart) && isInside(PAGES_DIR, target)) {
        const slug = slugFor(target);
        node.url = `${prefix}/${slug ? `docs/${slug}` : "docs"}/${suffix}`;
        return;
      }

      if (isInside(REPO_DIR, target)) {
        const rel = relative(REPO_DIR, target).split(sep).join("/");
        node.url = `${repo}/blob/${branch}/${rel}${suffix}`;
      }
    });
  };
}
