# gopi Site

The site uses Astro and Starlight. The landing page has its own layout. Its code comes from
`../examples/landing/`. `src/snippet.mjs` shows the lines between a
`// snippet:<name>` comment and its `// end` comment, so the Go toolchain
builds each line that the page shows. A snippet drops its error checks, so
each preview shows the calls that create and customize an agent.

The replay under the hero and the events figure come from a real run.
`make site-demo` builds `../examples/landing/demo`, a durable agent on a
scripted model. `scripts/record-demo.mjs` runs it, kills it during its third
tool call, and runs it again with no task. The script writes the output of
both runs and the events of the second run to `src/data/demo.json`. Run
`make site-demo` again after a change to the demo or to the events of an
agent. If a run gives an output that the page does not expect, the script
writes nothing.

Three figures move, and each one has the same content without JavaScript:
the replay types its lines again on a click, the events light up once in
order, and the layers figure moves to the next layer every 4 seconds until
the reader picks one. A radio picks the layer, so the figure works without
the script. With `prefers-reduced-motion`, nothing moves.
The pages live in `src/content/docs/`. The concept pages in the
repository's `docs/concepts/` are on the site too, under Concepts. The rest
of `docs/` is not part of the site. A link to a file that the site does not
show resolves to GitHub.

```
make site-install   # pnpm install
make site-dev       # dev server with hot reload
make site-build     # static build to web/dist
make site-check     # astro check
make site-test      # vitest
make docs-links     # every internal link in docs/ and the pages resolves
```

The rules for a page are in `../.claude/rules/site-docs.md`.

## Pages

The pages live in `src/content/docs/`. `src/content.config.ts` loads them
with a `docs/` ID prefix, which leaves `/` for the landing page. An
`index.mdx` file becomes the index of its directory. The sidebar is set in
`astro.config.mjs`.

The frontmatter controls the page introduction:

- `summary` supplies the page description and the text under the title.

`src/components/PageTitle.astro` renders these fields in place of Starlight's
page title component.

A guide imports its program from `../examples/<guide>/main.go` with a
`?raw` import and renders it with the Starlight `Code` component. The Go
toolchain compiles the program, so the page cannot show code that does not
build. `astro.config.mjs` allows the dev server to read the directory.

A page draws its picture with a figure from `src/components/figures/`. Each
form is a component, and the page gives it the content as props. The colors
are the tokens, so a figure follows the theme switch. `design/diagrams/`
holds the mockups, and `.claude/rules/site-docs.md` says how to pick a form.

`src/remark/rewrite-doc-links.mjs` rewrites a relative link to a page or a
concept page into a site route, and a relative link to any other file in the
repository into a link to GitHub. Authors write the link as a path to the file.
`scripts/check-links.mjs` makes sure that each link resolves.

## Concept Pages

`src/content.config.ts` also loads each Markdown file in `../docs/concepts/`.
GitHub and the site show the same files. A concept page keeps its path, so
`docs/concepts/ai/stream.md` is the route `/docs/concepts/ai/stream/`.

- `src/sidebar-concepts.mjs` builds the Concepts group from the index,
  `docs/concepts/README.md`. Each H2 of the index is a subgroup, with its
  pages in the order of its table.
- `src/remark/strip-title-heading.mjs` removes the H1 of a concept page,
  because `PageTitle.astro` shows the title. The H1 stays in the file for
  GitHub.
- `PageTitle.astro` shows the `description` of a concept page as the lede.
- `src/remark/render-mermaid.mjs` renders each Mermaid block of a concept
  page to an SVG with [Pele](https://pele.run) at build time. GitHub renders
  the same block with Mermaid. The SVG reads the `--pele-*` variables in
  `starlight.css`, so it follows the theme switch. A Mermaid block in a page
  stops the build.

Pele sizes each box from the width of its label. The plugin measures each
label in the font files of the site, from the `@fontsource` packages. If
`tokens.css` changes a font, change the font files in `astro.config.mjs`
too.

## Design System

[DESIGN.md](DESIGN.md) defines the shared rules for the landing page and the docs.
Shared values belong in `src/styles/tokens.css`. Layout-specific rules belong
in `landing.css` or `starlight.css`.

The site uses the palette of xdb.dev, with blue for interactive elements, IBM Plex Mono for headings and
code, and Noto Sans for prose. The design contract specifies spacing, surfaces,
and the differences between the layouts.

`src/shiki/ember.js` derives the syntax themes from the shared tokens. The themes
are registered in `ec.config.mjs` so the `<Code>` component can use them outside
Markdown pages.

## Deployment

`.github/workflows/site.yml` builds and deploys to GitHub Pages when a push to
`main` changes `web/` or `docs/`. `astro.config.mjs` uses the project-site base
`"/gopi"`. For a custom domain, set `base` to `"/"` and `site` to the domain.
