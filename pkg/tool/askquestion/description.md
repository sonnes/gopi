Ask the user one or more multiple-choice questions. Use this tool when you need a decision from the user before the current turn can continue.

Each call holds one to four related questions. Each question needs:

- a `header` that is unique and has 20 bytes or fewer
- a `question` that is complete and ends with `?`
- two to four `options`, each one an object with a short `label` and an optional `description`

Give only the options that you want to offer. Do not add an Other option, because the surface that shows the question can supply its own free-text choice. Set `multi_select` only when more than one option can apply. Put the recommended option first and add `(Recommended)` to its label.

The result holds one `{header, answer}` object for each question. A multi-select answer joins the labels with commas. A free-text answer holds the text that the user wrote.
