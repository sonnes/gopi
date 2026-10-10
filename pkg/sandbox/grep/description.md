Searches file contents with a regex (ripgrep syntax: no lookaround, no backreferences). Returns `path:line:text`.

<instruction>
- `path`: file or directory. Omitted → working directory.
- `glob` filters files: no slash (`*.go`) → file name at any depth; with slash (`src/**/*.ts`) → path from `path`.
- `ignore_case: true` for case-insensitive.
- Default 100 lines; raise with `limit`. Lines over 500 bytes are cut.
- Skips hidden files, symlinks, and `.gitignore`d files (with rg).
</instruction>

<critical>
- MUST use instead of shell `grep`/`rg`.
- Finding files by name → `find`.
</critical>
