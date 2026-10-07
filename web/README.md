# gopi Site

The site uses Astro and Starlight. The landing page has its own layout.
The pages live in `src/content/docs/`. The developer docs in the
repository's `docs/` directory are not part of the site. A page links to
one of them as a relative path, and the link resolves to GitHub.

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
`?raw` import and renders it with `src/components/CodeFrame.astro`. The Go
toolchain compiles the program, so the page cannot show code that does not
build. `astro.config.mjs` allows the dev server to read the directory.

A page draws its picture with a figure from `src/components/figures/`. Each
form is a component, and the page gives it the content as props. The colors
are the tokens, so a figure follows the theme switch. `design/diagrams/`
holds the mockups, and `.claude/rules/site-docs.md` says how to pick a form.

`src/remark/rewrite-doc-links.mjs` rewrites a relative link to a page into a
site route, and a relative link to any other file in the repository into a
link to GitHub. Authors write the link as a path to the file.
`scripts/check-links.mjs` makes sure that each link resolves.

## Design System

[DESIGN.md](DESIGN.md) defines the shared rules for the landing page and the docs.
Shared values belong in `src/styles/tokens.css`. Layout-specific rules belong
in `landing.css` or `starlight.css`.

The site uses coral for interactive elements, IBM Plex Mono for headings and
code, and Noto Sans for prose. The design contract specifies spacing, surfaces,
and the differences between the layouts.

`src/shiki/ember.js` derives the syntax themes from the shared tokens. The themes
are registered in `ec.config.mjs` so the `<Code>` component can use them outside
Markdown pages.

## Deployment

`.github/workflows/site.yml` builds and deploys to GitHub Pages when a push to
`main` changes `web/` or `docs/`. `astro.config.mjs` uses the project-site base
`"/gopi"`. For a custom domain, set `base` to `"/"` and `site` to the domain.
