// Expressive Code lives in its own config file so the <Code> component
// can be rendered outside markdown (the landing page uses it for every
// snippet). Starlight picks this up automatically.
import { emberDark, emberLight } from "./src/shiki/ember.js";

export default {
  // Theme type drives Starlight's default [data-theme='…'] selector, so
  // light/dark switch without extra wiring.
  themes: [emberLight, emberDark],
  // A long line wraps. The wrapped part keeps the indent of its line,
  // plus two columns, so it does not read as a new statement. A scrolled
  // line runs under the rounded edge of a docs panel, and the reader
  // cannot see that it continues. The landing page turns this off in
  // CodeFrame.astro.
  defaultProps: { wrap: true, preserveIndent: true, hangingIndent: 2 },
  styleOverrides: {
    // The landing page renders code through the same pipeline, so these
    // are the shared values. The docs round further and lighten the
    // border in starlight.css, which only that side of the site loads.
    borderRadius: "var(--r)",
    borderColor: "var(--line-strong)",
    codeFontFamily: "var(--font-mono)",
    codeFontSize: "0.79rem",
    codeLineHeight: "1.72",
    uiFontFamily: "var(--font-mono)",
    frames: {
      // The frame shadow comes from the site token instead, so a code
      // block casts the same shadow as a card.
      shadowColor: "transparent",
      // A title is a row on the code panel, not a tab: no fill of its
      // own, no indicator, and a hairline under the row.
      editorTabBarBackground: "var(--code-bg)",
      editorTabBarBorderColor: "transparent",
      editorTabBarBorderBottomColor: "var(--line)",
      editorTabsMarginInlineStart: "0",
      editorTabsMarginBlockStart: "0",
      editorActiveTabBackground: "transparent",
      editorActiveTabForeground: "var(--muted)",
      editorActiveTabBorderColor: "transparent",
      editorActiveTabIndicatorTopColor: "transparent",
      editorActiveTabIndicatorBottomColor: "transparent",
      editorActiveTabIndicatorHeight: "0px",
      // A terminal bar is the same row, with the dots in place of a
      // name. The default is a grey that Expressive Code derives.
      terminalTitlebarBackground: "var(--code-bg)",
      terminalTitlebarBorderBottomColor: "var(--line)",
      terminalTitlebarDotsForeground: "var(--line-strong)",
      terminalTitlebarDotsOpacity: "1",
      // The copy button and its tooltip take the site colours. The
      // defaults are a pure black border and a green of their own.
      inlineButtonForeground: "var(--muted)",
      inlineButtonBorder: "var(--line-strong)",
      inlineButtonBorderOpacity: "1",
      inlineButtonBackground: "var(--text)",
      inlineButtonBackgroundIdleOpacity: "0",
      inlineButtonBackgroundHoverOrFocusOpacity: "0.06",
      inlineButtonBackgroundActiveOpacity: "0.12",
      tooltipSuccessBackground: "var(--text)",
      tooltipSuccessForeground: "var(--ground)",
    },
    // Markers are the one place Expressive Code picks colours of its
    // own: it derives a blue for `mark` and a lime-and-salmon pair for
    // diffs, which are the only off-palette colours the site would
    // carry. Green confirms and red refuses here as everywhere else.
    textMarkers: {
      markBackground: "var(--primary-faint)",
      markBorderColor: "var(--primary)",
      insBackground: "color-mix(in srgb, var(--ok) 13%, transparent)",
      insBorderColor: "color-mix(in srgb, var(--ok) 55%, transparent)",
      insDiffIndicatorColor: "var(--ok)",
      delBackground: "color-mix(in srgb, var(--err) 11%, transparent)",
      delBorderColor: "color-mix(in srgb, var(--err) 50%, transparent)",
      delDiffIndicatorColor: "var(--err)",
    },
  },
};
