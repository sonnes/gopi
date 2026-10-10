Runs a command in bash in the working directory. Returns stdout, then stderr.

<instruction>
- Each call starts a new shell in the working directory; `cd` does not persist. Chain with `&&` in one call.
- `timeout` in seconds: default 300, max 3600.
- No terminal, no input: pass input in the command (`git commit -m "…"`). Prompts fail.
- Long-running programs (servers, watchers): redirect output to a file and end with `&`, e.g. `npm run dev > dev.log 2>&1 &`; read the file.
- Output keeps the last 2000 lines or 50KB. Truncated → footer names a file with the full output; read it with `read_file` or `grep`.
- Nonzero exit → error result with the output and the exit code.
</instruction>

<critical>
- NEVER use shell `grep`/`rg`; use `grep`.
- NEVER use shell `find`/`ls` to locate files; use `find`.
- NEVER use `cat`/`head`/`tail`/`sed` to read a file that `read_file` can read; use `read_file`.
- NEVER use `sed`/`echo >` to change files; use `edit_file` or `write_file`.
</critical>
