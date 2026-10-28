// Ember syntax themes.
//
// Four hues, and each one means something:
//   rose   the language itself — func, for, range, defer, return
//   blue   a name that comes from a package — a type or a function
//   green  literal text
//   amber  a value the language names — a number, nil, true, false
// Everything else is ink, punctuation is one step back from it, and a
// comment is grey. Colour does the work, so no token is bold.
//
// The values are literals because Shiki cannot read CSS variables. They
// are also the values that ship: Expressive Code raises any token below
// 5.5:1 against the background, so a colour picked under that threshold
// would be silently rewritten and this file would stop describing the
// page. src/shiki/ember.test.js holds each one above it.

const light = {
  bg: "#ffffff", // --code-bg
  fg: "#15171c", // --text
  comment: "#5f6470", // --muted
  punct: "#4a4f5a", // --muted-strong
  keyword: "#be123c", // --l-ai
  name: "#075985", // --l-wire-deep
  literal: "#065f46", // --l-durable-deep
  constant: "#92400e", // --l-agent-deep
};

// On a dark ground the deep end is the unreadable one, so each hue takes
// the lifted value the layer tokens already use there.
const dark = {
  bg: "#16181d", // --code-bg
  fg: "#eceef3",
  comment: "#8b909c",
  punct: "#a3a8b3",
  keyword: "#fb7185",
  name: "#38bdf8",
  literal: "#34d399",
  constant: "#f59e0b",
};

function theme(name, type, c) {
  return {
    name,
    type,
    colors: {
      "editor.background": c.bg,
      "editor.foreground": c.fg,
    },
    settings: [
      {
        scope: ["comment", "punctuation.definition.comment"],
        settings: { foreground: c.comment },
      },
      {
        // Separators and brackets only. A string's own quotes belong to
        // the string, and are coloured with it below.
        scope: [
          "punctuation.separator",
          "punctuation.terminator",
          "punctuation.section",
          "meta.brace",
        ],
        settings: { foreground: c.punct },
      },
      {
        // Every part of a literal, so an escape inside a string does not
        // break the run of colour.
        scope: [
          "string",
          "string.quoted",
          "punctuation.definition.string",
          "constant.character.escape",
        ],
        settings: { foreground: c.literal },
      },
      {
        scope: [
          "constant.numeric",
          "constant.language",
          "constant.other.symbol",
          "variable.language",
        ],
        settings: { foreground: c.constant },
      },
      {
        scope: [
          "keyword",
          "keyword.control",
          "keyword.operator.new",
          "storage",
          "storage.type",
          "storage.modifier",
        ],
        settings: { foreground: c.keyword },
      },
      {
        // Named in full rather than left to the grammar, so a type and
        // the function beside it cannot land on two different hues.
        scope: [
          "entity.name.type",
          "entity.name.class",
          "entity.name.struct",
          "entity.name.function",
          "support.type",
          "support.class",
          "support.function",
        ],
        settings: { foreground: c.name },
      },
      {
        scope: ["variable", "variable.other", "meta.definition.variable"],
        settings: { foreground: c.fg },
      },
    ],
  };
}

export const emberLight = theme("ember-light", "light", light);
export const emberDark = theme("ember-dark", "dark", dark);
