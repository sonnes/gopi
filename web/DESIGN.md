# Design

The landing page and docs share this design contract. Stylesheets must follow
these rules.

The stylesheets divide responsibility as follows:

| Sheet           | Holds                                              |
| --------------- | -------------------------------------------------- |
| `tokens.css`    | Every value the system names, and the shared chrome |
| `landing.css`   | The landing page only                               |
| `starlight.css` | The docs only - maps the tokens onto Starlight      |

Shared values belong in `tokens.css`. Layout sheets must not redefine tokens.

## Principles

Use a 1px `--line` to separate elements. Fills are limited to callouts, cards,
code blocks and figures within prose, and text input controls. These fills distinguish
content from surrounding prose or identify an input field.

Mark the current sidebar page or contents heading with color and weight.

The landing page uses butter yellow for its primary action. Other content stays on the page background.

Coral identifies clickable elements. Within code blocks, it identifies keywords.

Use the display, title, prose, label, or code type style. New sizes and weights must
fit one of these styles.

Use the spacing scale: 8, 16, 24, 40, 64.

## Type

| Voice   | Face          | Size            | Weight | Tracking | Colour          |
| ------- | ------------- | --------------- | ------ | -------- | --------------- |
| display | IBM Plex Mono | clamped, ≥1.2rem | 700    | −0.045em | `--text`        |
| title   | IBM Plex Mono | 0.9rem          | 600    | 0        | `--text`        |
| prose   | Noto Sans     | 0.94–1.05rem    | 400    | 0        | `--copy`        |
| label   | IBM Plex Mono | 0.78rem         | 400    | +0.02em  | `--muted`       |
| code    | IBM Plex Mono | 0.8rem          | 400    | 0        | per the theme   |

The display style uses IBM Plex Mono through `--font-display`. It applies to
headings. The title style applies to the title of a card, an aside, and a
pagination link. A sidebar group heading takes the mono at the title weight,
at the size of a sidebar link, in `--text`. It keeps the case of its name.
A sidebar link uses the prose face. A top-level link takes the title
weight and `--text`.
The wordmark uses `src/assets/gopi-wordmark.png`, with rounded brown letters and a coral dot above the “i”.
The hero uses `src/assets/agent-gopi-wordmark.png`, which combines both words in one illustration. Noto Sans
distinguishes prose from display text.

The label style applies to section labels, filenames, table headings, footer
metadata, the contents heading, and the group name above a page title. Use
lowercase text with light tracking.

In the docs, a paragraph or a list is 31rem wide at most, about 70 characters.
Code, figures, and tables use the full reading column.

Set a label through `--label-size` and `--label-track`, never by typing
the numbers again.

## Colour

Use deep brown (`#3C3028`) on the near-white background (`#FFFEFC`). Do not use pure black.
Butter yellow (`#F4C65D`) colors the mascot and primary action. Coral (`#DE7356`) colors accents.
Use the darker `--link` color for readable links. The dark background is `#141110`.

| Token          | Job                                          |
| -------------- | -------------------------------------------- |
| `--text`       | Headings, code, the cell of a table           |
| `--copy`       | Running prose                                 |
| `--muted`      | Labels, and the dim column of a ledger        |
| `--code-bg`    | The panel under a code block, and a node in a figure |
| `--line`       | Every division on the page                    |
| `--line-strong` | The one rule under a table head, and a field  |
| `--primary`    | Fills and marks                               |
| `--link`       | Text you can click - a fill is too light to read at 14px |
| `--scrim`      | The dim layer under a dialog                   |

A focused element has a 2px `--primary` outline, 2px outside its edge. On
white, the ring is 3.1:1, which is more than the 3:1 that a focus ring needs.
The copy button and its tooltip use the site colors, not the Expressive Code
defaults.

Every card icon takes the accent. Starlight gives each card of a grid its own
hue, and the site does not use those hues.

Syntax colour is four hues, each with a meaning. `src/shiki/ember.js`
holds the values and `src/shiki/ember.test.js` keeps every one of them
above 5.5:1, which is the ratio below which Expressive Code silently
rewrites a colour.

| Hue   | Means                                            |
| ----- | ------------------------------------------------ |
| coral | The language - `func`, `for`, `range`, `defer`    |
| blue  | A name from a package - a type or a function      |
| green | Literal text                                      |
| amber | A value the language names - a number, `nil`      |

Use color to distinguish syntax. Do not use bold text in code blocks.

## Surface

Corner radius identifies panels.

| Token    | On                                              |
| -------- | ----------------------------------------------- |
| `--r`    | The landing page's corner, where it has one     |
| `--r-lg` | A card                                          |
| `--r-xl` | A callout, and a code block or figure in prose |

The primary landing action uses a small, solid brown offset shadow. Other content in the flow casts no shadow. `--shadow` is for the things that
float over the page: the search dialog, the skip link, the mobile
contents.

## Layout

| Token         | Value    | Job                                    |
| ------------- | -------- | -------------------------------------- |
| `--shell`     | 78rem    | The outer edge, on both layouts        |
| `--shell-pad` | 2.5rem   | The edge of the sheet                  |
| `--gutter`    | 2.5rem   | Between two columns                    |
| `--band`      | 4rem     | Above and below a band                 |
| `--measure`   | 27rem    | A line of prose, about 57 characters   |
| `--split`     | 5fr / 7fr | Words against code                    |

The wider code column leaves room for examples while keeping prose lines short.

## The Figure

A figure is the picture of a docs page. Its form comes from the question that
the page answers: a timeline for an order, a matrix for a choice, annotated
code for the parts of a call. Each form is a component in
`src/components/figures/`. `design/diagrams/` holds the mockups, and
`design/styles/` holds the looks that were tried.

`Figure.astro` draws the panel of every form: `--panel`, a `--line` border,
and `--r-xl`. The panel is `.not-content`, so the prose rules do not reach
the drawing.

A form draws with the parts in the figures section of `starlight.css`:

| Part    | Is                                                              |
| ------- | --------------------------------------------------------------- |
| `.node` | A box: `--code-bg`, a 1.2px `--line-strong` stroke, an 8px corner |
| `.chip` | A pill: an entry, an event, or a badge                           |
| `.code` | Lines of code in the four syntax hues                            |
| `.pin`  | A numbered `--primary` mark that ties code to a note             |
| `.lbl`  | The label style                                                  |

A kind colors a node or a chip by what it holds. A kind takes a layer hue
as its stroke, and the same hue at `--l-mix` over `--code-bg` as its fill:

| Kind        | Stroke          | Fill and line                           |
| ----------- | --------------- | --------------------------------------- |
| `user`      | `--line-strong` | `--code-bg`                             |
| `assistant` | `--l-agent`     | tint                                    |
| `tool`      | `--l-wire`      | tint                                    |
| `meta`      | `--l-durable`   | tint, dashed                            |
| `value`     | `--l-harness`   | tint                                    |
| `ephemeral` | `--muted`       | `--code-bg`, dotted, `--muted` label    |
| `error`     | `--err`         | tint, dashed, `--err` label             |

`--primary` marks the one thing that the figure is about. Text in a figure
is set in the mono at 11px or 12px. Coral text means a link, so a figure
marks its subject with a coral line or a coral stroke.

A figure fits the reading column, 552px inside the panel. A wider figure
scrolls sideways, and it fades at the edge where more is hidden. The fade
goes when the reader scrolls to the end. The rules of one form are in the
scoped style of its component, and they use tokens only.

## The Table

Tables have no outer border, corner radius, or cell fill. Use the label style
for headings, with a `--line-strong` rule underneath. Separate rows with `--line`
and align the first cell with adjacent text. Use `tabular-nums` for numbers.

These table rules apply to both layouts, including capability matrices.

In the docs, a table fills the reading column. Below 50rem, a table keeps its
own width, scrolls sideways, and fades at the hidden edge.

## Layout Differences

The layouts differ in their treatment of code blocks and fills:

On the landing page, code occupies its own column without a frame. The filename
sits on a hairline. In the docs, code interrupts prose and uses a frame with
`--r-xl`.

In the docs, a long line wraps. The wrapped part keeps the indent of its line,
plus two columns. The full program at the end of a guide stops at 32rem and
scrolls, with a fade at the bottom edge. A title
or a terminal bar is a row at the top of the code panel, with a hairline under
it. A file name in that row uses the label style. On the landing page, a line does not wrap.

Docs callouts and cards use fills to separate them from prose. The landing
page uses no fills. A docs card holds one link, and the whole card takes the
click.

## Adding to the System

1. Choose an existing type style.
2. Use the spacing scale.
3. Use a hairline unless the element qualifies for a fill.
4. Put named values in `tokens.css`.

Put layout-specific rules in the stylesheet for that layout.

## Mascot

Agent Gopi is a golden gopher with lowered sunglasses, a cream trench coat, and a coral tie.
The landing hero uses `src/assets/agent-gopi.png`. Preserve its transparent background and full silhouette.
The SVG face marks appear in navigation and browser tabs. Keep both theme variants consistent.

## Sidebar Illustrations

Docs pages use contextual pencil sketches of Agent Gopi below the table of contents.
`DocsTableOfContents.astro` maps each page to a sketch in `src/assets/docs/`. Related guides share a sketch.
Use dark, slightly irregular, overlapping strokes for each outline. All backgrounds and interiors are transparent.
Do not add fills, shading, sunglasses, a card, or a caption. Dark mode inverts the stroke color.
Use generated pencil artwork saved as PNGs. Do not use Rough.js to draw these illustrations.
The illustrations appear only in the desktop sidebar, at widths of 72rem or more.
The peeking sketch uses a pencil divider as the scene edge on Get Started and Messages.
The illustrations do not replace technical diagrams.
