Reads a file via `path`: text, images (png, jpeg, gif, webp), PDF.

<instruction>
- Text: up to 2000 lines or 50KB per call. Footer names the next `offset`; re-issue with it to continue.
- `offset` is 1-indexed. `limit` caps lines.
- Images and PDF: returned as attachments; `offset`/`limit` ignored.
- Read only the range you need.
</instruction>

<critical>
- NEVER use `bash` (`cat`/`head`/`tail`/`sed`) to read a file that `read_file` can read.
- Find files with `find`, not `read_file`.
</critical>
