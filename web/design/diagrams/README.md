# Figure Mockups

This directory holds the mockups of the figures on the docs pages. Each file in `parts/` is one page. Each slot of a page shows two or three forms. Option A is the form that the site uses. B and C are the forms that were not chosen, and they stay here for the next page.

The site builds each chosen form as a component in `web/src/components/figures/`. `.claude/rules/site-docs.md` says how to pick a form, and `web/DESIGN.md` has the rules for a figure.

## Open the Mockups

1. Run `./build.sh`. It writes `index.html` from `intro.html` and every part.
2. Open `index.html` in a browser. The theme button changes between light and dark.

## Add a Page

1. Copy `parts/_example.html` to `parts/<id>.html`.
2. Add `<id>` to `ORDER` in `build.sh`.
3. Add one slot for each figure of the page. Draw two or three forms in each slot, and tag the recommended form `A`.
4. Run `./build.sh --shot parts/<id>.html`. It writes a light and a dark screenshot to `shots/`.
5. When a form is chosen, build it as a component. Then change the tag of the chosen form to `A · on the site`.

Rules for a part:

- Draw each figure at the width of the docs column, 552px inside the panel.
- Use only the tokens of `web/src/styles/tokens.css`, so dark mode works.
- Start every selector in a part with `#<id>`, so the parts do not change each other.
- Use the real names from the page and from `pkg/`.

## Take a Screenshot of a Page

`shot.mjs` takes a screenshot of a page from the dev server, in light or dark. With a CSS selector, it crops the screenshot to the matching elements.

```sh
node shot.mjs http://localhost:4321/gopi/docs/ai/streams/ streams.png dark 1280 .fig
```

`build.sh` and `shot.mjs` use Google Chrome at its macOS path.
