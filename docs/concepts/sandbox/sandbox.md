---
title: Sandbox
description: How the file and shell tools reach a directory on this host or a remote machine through one small interface.
package: pkg/sandbox
read_when:
  - You give an agent tools that read files, change files, or run commands
  - You implement a new sandbox, such as a remote VM
  - A tool reads or writes a path that you did not expect
---

# Sandbox

A sandbox is the place where the tools read files, write files, and run commands. The tools use only the `Sandbox` interface, so one set of tools works on this host and on a remote one.

```
  read_file  write_file  edit_file  bash  grep  find
      |          |           |       |     |     |
      +----------+-----------+---+---+-----+-----+
                                 |
                         Sandbox interface
                                 |
                  +--------------+--------------+
                  |                             |
                Local                   a remote sandbox
```

## Design

- The tools take a sandbox, not an `io/fs.FS`. An `fs.FS` has no context, and a walk costs one call for each file on a remote sandbox.
- The interface is small, so a remote sandbox needs one call for each method. The cost: read_file cannot check the size of a remote file before it reads the whole file.
- The sandbox resolves a path and applies its fence policy, not the tool, because a remote sandbox is isolated already and knows its own root.
- `Local` without a fence policy has no limits: a `../` path or a symlink reaches every file of the user. A default fence blocks the network and the toolchains that most agents need, so a caller states its limits in one fence policy.
- The file tools check each path against the file rules of the [fence](fence.md), so a file tool cannot read or write a path that a fenced command cannot.
- After the open, the tool checks the path of the open file again, because a fenced command can change a symlink after the check. The cost: a write through a symlink to a missing file fails, and a write to a file with two hard links fails.
- If the fence cannot run, `Local` returns an error, because the caller thinks that the commands are confined.
- The grep and find tools run rg inside the sandbox, because a remote walk costs one call for each file. Without rg, a local sandbox walks the directory in Go, and a remote sandbox returns an error. The cost: rg skips the files that `.gitignore` names and the walk does not.
- The edit and write tools lock the absolute path, because the [loop](../agent/loop.md) runs the calls of one message at the same time. A bash command, another process, and a symlink to the file do not take the lock.
- The file tools keep no record of the reads of the model, so a tool has no state across calls. The cost: write_file replaces a file that the model never read.
- Each tool is its own package, so a caller imports only the tools that it gives the agent. The tools take no options, because a [tool](../tool/tool.md) middleware renames a tool or changes its description.

## Map

| Task                                        | File                                  |
| ------------------------------------------- | ------------------------------------- |
| Implement a new sandbox                     | `pkg/sandbox/sandbox.go`              |
| Change how a local sandbox applies a fence policy | `pkg/sandbox/local.go` |
| Add a tool                                  | a new package in `pkg/sandbox/`, `pkg/harness/builtin.go`, `pkg/harness/permission/kind.go`, and `cmd/gopi/main.go` |
| Change the text that the model reads        | `description.md` in the package of the tool |
| Change the rg arguments or the walk         | `pkg/sandbox/internal/search/search.go`, `grep/grep.go`, `find/find.go` |

## Related Concepts

- [Tool](../tool/tool.md)
- [Fence](fence.md)
