import { readdirSync, readFileSync } from "node:fs";

// The sidebar group of the concept pages. Starlight can generate a group
// only from src/content/docs, and the concept pages are in /docs/concepts.
//
// The index, docs/concepts/README.md, has one H2 per layer and a table of
// links under it. The group follows the index: one subgroup per H2, with
// the pages in the order of the table. A page that the index does not
// list goes to the end of the subgroup of its directory, so each page
// gets an entry.

const CONCEPTS = new URL("../../docs/concepts/", import.meta.url);

function indexLayers() {
  const text = readFileSync(new URL("README.md", CONCEPTS), "utf8");
  const layers = [];
  for (const line of text.split("\n")) {
    const heading = line.match(/^## (\S+)\s*$/);
    if (heading) {
      layers.push({ name: heading[1], pages: [] });
      continue;
    }
    const layer = layers.at(-1);
    if (!layer) continue;
    for (const [, page] of line.matchAll(/\]\(([\w-]+\/[\w-]+)\.md\)/g)) {
      if (!layer.pages.includes(page)) layer.pages.push(page);
    }
  }
  return layers;
}

function pagesOnDisk() {
  return readdirSync(CONCEPTS, { recursive: true })
    .filter((f) => /^[\w-]+\/[\w-]+\.md$/.test(f.split("\\").join("/")))
    .map((f) => f.split("\\").join("/").replace(/\.md$/, ""))
    .sort();
}

export function conceptsGroup() {
  const onDisk = pagesOnDisk();
  const layers = indexLayers();

  for (const page of onDisk) {
    if (layers.some((l) => l.pages.includes(page))) continue;
    const name = page.split("/")[0];
    let layer = layers.find((l) => l.name === name);
    if (!layer) {
      layer = { name, pages: [] };
      layers.push(layer);
    }
    layer.pages.push(page);
  }

  return {
    label: "Concepts",
    collapsed: true,
    items: [
      { label: "All Concepts", slug: "docs/concepts" },
      ...layers.map((layer) => ({
        label: layer.name,
        collapsed: true,
        items: layer.pages
          .filter((page) => onDisk.includes(page))
          .map((page) => ({ slug: `docs/concepts/${page}` })),
      })),
    ],
  };
}
