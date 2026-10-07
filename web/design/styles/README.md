# Figure Styles

This directory holds the looks that were tried for the figures. Each file in `parts/` draws the same three figures in one look: the events of a stream, a session scenario, and the failure channels of a tool.

| Part        | Look                                                       |
| ----------- | ---------------------------------------------------------- |
| `ink`       | A figure in a book: ink lines, serif labels, italic notes   |
| `text`      | Box-drawing characters in a `<pre>`                         |
| `swiss`     | Thick rules, square blocks, and the colors of the landing page |
| `schematic` | A dot grid, square wires, and a title block                 |
| `annotated` | The real output, file, or code, with notes in the margin    |

The site tried the ink elements and went back to its own look. `web/DESIGN.md` has the rules.

To see the looks, run `./build.sh`. Then open `index.html` in a browser.
