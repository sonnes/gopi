Finds files by glob. Returns one path per line, newest first.

<instruction>
- No slash (`*.go`) → file name at any depth; with slash (`src/**/*.ts`) → path from `path`.
- `path`: directory to search. Omitted → working directory.
- Default 1000 paths; change with `limit`.
- Skips hidden files, symlinks, and `.gitignore`d files (with rg).
</instruction>

<critical>
- MUST use instead of shell `find`/`ls`.
- Searching file contents → `grep`.
</critical>
