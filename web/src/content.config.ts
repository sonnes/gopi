import { defineCollection } from "astro:content";
import { z } from "astro/zod";
import { glob } from "astro/loaders";
import { docsSchema } from "@astrojs/starlight/schema";

// The site has two sources. The pages for the users of gopi live in
// src/content/docs. The concept pages live in the repository's
// /docs/concepts, so GitHub and the site show the same files. The rest of
// /docs is not part of the site.
//
// IDs get a "docs/" prefix to leave "/" free for the landing page. A page
// keeps its path under src/content/docs. A concept page keeps its path
// under /docs. index.mdx and README.md become the index of their directory.
const PAGES = "web/src/content/docs/";

export const collections = {
  docs: defineCollection({
    loader: glob({
      base: "..",
      pattern: [`${PAGES}**/*.{md,mdx}`, "docs/concepts/**/*.md"],
      generateId: ({ entry }) => {
        const path = entry
          .replace(PAGES, "docs/")
          .replace(/\.mdx?$/, "")
          .replace(/(^|\/)(index|README)$/, "")
          .replace(/\/$/, "");
        return path || "docs";
      },
    }),
    schema: (ctx) =>
      docsSchema({
        extend: z.object({
          // The frontmatter convention of the pages. `summary` becomes
          // the lede.
          summary: z.string().optional(),
          // The frontmatter convention of the concept pages. See
          // .claude/rules/concepts.md. `description` becomes the lede.
          package: z.string().optional(),
          read_when: z.array(z.string()).optional(),
        }),
      })(ctx).transform((data) => ({
        ...data,
        description: data.description ?? data.summary,
      })),
  }),
};
