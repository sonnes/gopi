// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import { unified } from "@astrojs/markdown-remark";
import { fileURLToPath } from "node:url";

import { renderMermaid } from "./src/remark/render-mermaid.mjs";
import { rewriteDocLinks } from "./src/remark/rewrite-doc-links.mjs";
import { stripTitleHeading } from "./src/remark/strip-title-heading.mjs";
import { conceptsGroup } from "./src/sidebar-concepts.mjs";

// Project-site defaults for GitHub Pages. With a custom domain, set
// base to "/" and site to the domain.
const BASE = "/gopi";
const REPO = "https://github.com/sonnes/gopi";
// The cover of the site, from web/design/og/og.html. Each page names it
// as its social card, so a link to any page shows it.
const COVER = `https://sonnes.github.io${BASE}/og.png`;

// The guides import their programs from ../examples with ?raw, so the
// dev server has to be allowed to read outside web/.
const ROOT = fileURLToPath(new URL("..", import.meta.url));

export default defineConfig({
  site: "https://sonnes.github.io",
  base: BASE,
  trailingSlash: "always",
  vite: {
    server: { fs: { allow: [ROOT] } },
  },
  markdown: {
    processor: unified({
      remarkPlugins: [
        stripTitleHeading,
        [
          renderMermaid,
          {
            // The fonts of tokens.css. The diagram labels are measured in them.
            fonts: {
              regular: "@fontsource/noto-sans/files/noto-sans-latin-400-normal.woff",
              bold: "@fontsource/noto-sans/files/noto-sans-latin-700-normal.woff",
              mono: "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-400-normal.woff",
              monoBold: "@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-700-normal.woff",
            },
            // No maxWidth: Pele breaks an identifier, such as
            // EventApprovalStart, in the middle to fit a width.
          },
        ],
        [rewriteDocLinks, { base: BASE, repo: REPO }],
      ],
    }),
  },
  integrations: [
    starlight({
      title: "gopi",
      // The documentation uses the shared image wordmark.
      logo: {
        src: "./src/assets/gopi-wordmark.png",
        replacesTitle: true,
        alt: "gopi",
      },
      favicon: "/favicon.svg",
      description:
        "Build AI agents in Go with model calls, typed tools, and streamed events.",
      customCss: ["./src/styles/tokens.css", "./src/styles/starlight.css"],
      // The docs pages do not go through Landing.astro, so the fonts have
      // to be requested here as well.
      head: [
        {
          // Light is the default, on the docs as on the landing page. This
          // seeds Starlight's own storage key before its theme script reads
          // it, so a dark OS preference does not decide for the reader.
          // The toggle still works and still wins.
          tag: "script",
          content:
            "try{if(!localStorage.getItem('starlight-theme'))localStorage.setItem('starlight-theme','light')}catch(e){}",
        },
        {
          tag: "link",
          attrs: { rel: "preconnect", href: "https://fonts.googleapis.com" },
        },
        {
          tag: "link",
          attrs: {
            rel: "preconnect",
            href: "https://fonts.gstatic.com",
            crossorigin: true,
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "stylesheet",
            href: "https://fonts.googleapis.com/css2?family=Lilita+One&family=IBM+Plex+Mono:wght@400;500;600&family=Noto+Sans:wght@400;500;600;700;800&display=swap",
          },
        },
        { tag: "meta", attrs: { property: "og:image", content: COVER } },
        { tag: "meta", attrs: { name: "twitter:card", content: "summary_large_image" } },
        { tag: "meta", attrs: { name: "twitter:image", content: COVER } },
      ],
      components: {
        TableOfContents: "./src/components/DocsTableOfContents.astro",
        // Renders the sidebar group as an eyebrow and the summary as a lede.
        PageTitle: "./src/components/PageTitle.astro",
        // Adds the edit and issue chips above the prev-next pair.
        Footer: "./src/components/Footer.astro",
        // Reads the destination before the direction.
        Pagination: "./src/components/Pagination.astro",
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: REPO,
        },
      ],
      // An entry path is relative to web/. A concept page starts with
      // "../docs/", which resolves to the docs directory of the repository.
      editLink: {
        baseUrl: `${REPO}/edit/main/web/`,
      },
      // Expressive Code options live in ec.config.mjs. That file is
      // required for the <Code> component to render outside markdown.
      sidebar: [
        { label: "Overview", slug: "docs" },
        { label: "Get Started", slug: "docs/start" },
        {
          label: "pkg/ai",
          items: [
            { label: "The Layer", slug: "docs/ai" },
            { label: "Models", slug: "docs/ai/models" },
            { label: "Messages", slug: "docs/ai/messages" },
            { label: "Options", slug: "docs/ai/options" },
            { label: "Streams", slug: "docs/ai/streams" },
          ],
        },
        {
          label: "pkg/tool",
          items: [{ label: "Tools", slug: "docs/tool" }],
        },
        {
          label: "pkg/agent",
          items: [
            { label: "Agents", slug: "docs/agent" },
            { label: "Events", slug: "docs/agent/events" },
            { label: "Hooks", slug: "docs/agent/hooks" },
            { label: "Steer and Follow-Up", slug: "docs/agent/steer" },
            { label: "Sections", slug: "docs/agent/sections" },
            { label: "Tool Changes", slug: "docs/agent/tool-changes" },
            { label: "CLI Agents", slug: "docs/agent/cli" },
          ],
        },
        {
          label: "pkg/harness",
          items: [{ label: "Harness", slug: "docs/harness" }],
        },
        {
          label: "pkg/session",
          items: [{ label: "Sessions", slug: "docs/session" }],
        },
        {
          label: "pkg/host",
          items: [{ label: "Host", slug: "docs/host" }],
        },
        {
          label: "Providers",
          items: [
            { label: "Anthropic", slug: "docs/providers/anthropic" },
            { label: "OpenAI", slug: "docs/providers/openai" },
            { label: "OpenAI Chat", slug: "docs/providers/openai-chat" },
          ],
        },
        {
          label: "Guides",
          items: [
            { label: "Call a Model", slug: "docs/guides/call-a-model" },
            { label: "Stream a Response", slug: "docs/guides/stream-a-response" },
            { label: "Use Tools", slug: "docs/guides/use-tools" },
            { label: "Write a Provider", slug: "docs/guides/write-a-provider" },
            { label: "Run an Agent", slug: "docs/guides/run-an-agent" },
            { label: "Keep a Session", slug: "docs/guides/keep-a-session" },
          ],
        },
        conceptsGroup(),
        {
          label: "GoDoc",
          link: "https://pkg.go.dev/github.com/sonnes/gopi",
          attrs: { target: "_blank" },
        },
      ],
    }),
  ],
});
