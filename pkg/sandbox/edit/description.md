Replaces exact text in one file.

<instruction>
- MUST use the smallest `old_string` that identifies the change uniquely.
- `old_string` must occur once. Not unique → add surrounding lines, or set `replace_all: true` to change every occurrence.
- Rename across a file → `replace_all: true`.
- Copy text exactly as `read_file` showed it, with indentation.
</instruction>

<critical>
- MUST `read_file` before editing; NEVER edit from memory.
- Several changes → one call each.
</critical>
