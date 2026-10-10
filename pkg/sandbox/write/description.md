Creates or overwrites a file at `path`. Creates parent directories.

<conditions>
- Creating new files the task requires
- Replacing whole content when `edit_file` would be more complex
</conditions>

<critical>
- SHOULD use `edit_file` to change part of an existing file.
- NEVER create documentation files (*.md, README) unless asked.
</critical>
