import { describe, expect, it } from "vitest";

import { emberDark, emberLight } from "./ember.js";

// Relative luminance and contrast, per WCAG 2.1.
function luminance(hex) {
  const channels = [1, 3, 5]
    .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

function colorFor(theme, scope) {
  const rule = theme.settings.find((s) => s.scope.includes(scope));
  return rule?.settings.foreground;
}

// Expressive Code lightens or darkens any token that falls below this
// against the editor background, so a colour picked under it would be
// silently rewritten and the theme file would stop describing the page.
const MIN_CONTRAST = 5.5;

describe.each([
  ["ember-light", emberLight],
  ["ember-dark", emberDark],
])("%s", (_name, theme) => {
  const bg = theme.colors["editor.background"];

  it("keeps every token above the threshold Expressive Code rewrites at", () => {
    const foregrounds = theme.settings
      .map((s) => s.settings.foreground)
      .concat(theme.colors["editor.foreground"]);

    for (const fg of foregrounds) {
      expect(contrast(fg, bg), `${fg} on ${bg}`).toBeGreaterThanOrEqual(
        MIN_CONTRAST,
      );
    }
  });

  it("gives the keyword, the literal, the name and the constant a hue each", () => {
    const hues = {
      keyword: colorFor(theme, "keyword"),
      string: colorFor(theme, "string"),
      name: colorFor(theme, "entity.name.type"),
      constant: colorFor(theme, "constant.numeric"),
      comment: colorFor(theme, "comment"),
    };

    for (const [role, hue] of Object.entries(hues)) {
      expect(hue, role).toBeDefined();
      expect(hue, `${role} is the plain foreground`).not.toBe(
        theme.colors["editor.foreground"],
      );
    }

    expect(new Set(Object.values(hues)).size).toBe(Object.keys(hues).length);
  });
});
