import { defineCollection, z } from "astro:content";
import { glob } from "astro/loaders";
import { docsSchema } from "@astrojs/starlight/schema";

// The pages of the site live in src/content/docs. The developer docs
// in the repository's /docs directory are not part of the site.
//
// IDs get a "docs/" prefix to leave "/" free for the landing page, and
// index.mdx becomes the index of its directory.
export const collections = {
  docs: defineCollection({
    loader: glob({
      base: "./src/content/docs",
      pattern: "**/*.{md,mdx}",
      generateId: ({ entry }) => {
        const path = entry
          .replace(/\.mdx?$/, "")
          .replace(/(^|\/)index$/, "")
          .replace(/\/$/, "");
        return path ? `docs/${path}` : "docs";
      },
    }),
    schema: (ctx) =>
      docsSchema({
        extend: z.object({
          // The frontmatter convention of the pages. `summary` becomes
          // the lede.
          summary: z.string().optional(),
        }),
      })(ctx).transform((data) => ({
        ...data,
        description: data.description ?? data.summary,
      })),
  }),
};
