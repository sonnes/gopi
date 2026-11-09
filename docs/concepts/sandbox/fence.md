---
title: Fence
description: How a command runs inside an OS sandbox that limits its writes, its reads, and its network.
package: pkg/sandbox/fence
read_when:
  - You confine the commands that a model runs on this host
  - A command fails with "Operation not permitted" inside a fence
  - You add a backend for another OS
---

# Fence

A fence runs a command inside an OS sandbox. The command and its children can write only to the write directories. They cannot read the hidden paths, and they have no network unless the fence policy turns it on. On macOS, the backend is Seatbelt: the fence runs each command through `sandbox-exec` with a generated profile.

```
  The profile, in order. A later rule wins.

  base.sb         deny default; allow exec, fork, a few sysctls
  allow           file-read* everywhere
  preferences.sb  reads of the user preferences
  allow           file-write* in each Write directory
  network.sb      only with Network(true)
  deny            XPC services, two fcntls; allow /dev/fd
  deny            writes to ReadOnly paths, renames of parents
  deny            writes to ReadOnlyNames, renames of their dirs
  deny            reads and writes of Hide paths
```

## Design

- The profile denies by default. An allow-default profile blocks writes but leaves system services open, and a service such as LaunchServices starts a process outside the sandbox. The base rules come from the [Seatbelt policy of OpenAI Codex](https://github.com/openai/codex/blob/696b4502dfaacd91133c3691a23dc91ae3b14bc9/codex-rs/sandboxing/src/seatbelt_base_policy.sbpl), under the Apache License 2.0.
- A symlink in a write directory does not lead a write outside.
- A program outside the fence that later runs a file from a write directory runs it without limits, so `Workspace` keeps each file that git, a shell, or an editor runs read-only, at the root and in each directory under it. The list `ProtectedNames` joins the lists of [sandbox-runtime](https://github.com/anthropics/sandbox-runtime/blob/22b545aa6661491bcb911e7239a8d29b17de0bae/src/sandbox/sandbox-utils.ts) and Claude Code, with `.git/commondir` added. The cost: `git init`, `git remote add`, and a Yarn install that writes `.yarn` fail inside the fence.
- The fence owns the path rules: the list of protected names, the real path of a path, and the test for a path in a folder. The [permission policy](../permission/permission.md) uses the same rules, so the fence and the policy protect the same files and resolve a symlink in the same way.
- `Workspace` takes more roots after the first one. Each root gets the same rules for git, so an agent can add a worktree of a clone in another root and cannot change the hooks of that clone.
- A read-only path also protects each parent that a command can rename, up to the outermost write directory. Otherwise, a rename of a root in the temp directory moves `.git/hooks` out of the rule.
- `Workspace` limits the writes and leaves the reads open, because compilers, interpreters, and package managers read their toolchains from many folders, also from the home directory. The cost: a command can read each file of the user, also the keys in `~/.ssh` and `~/.aws`. To hide a path, add `Hide` after the policy.
- `Workspace` puts the build caches in the temp directory, because a program outside the fence reads the cache directory of the user.
- With the network on, a command cannot connect to a unix socket, except the resolver of macOS. A socket reaches a process outside the fence, such as Docker.
- `Workspace` turns the network on and keeps the whole environment, because builds and package managers need both.

## Map

| Task                                     | File                                     |
| ---------------------------------------- | ---------------------------------------- |
| Change the options or the fence policy of Workspace | `pkg/sandbox/fence/fence.go` |
| Change the protected names or the path rules | `pkg/sandbox/fence/fence.go` |
| Change how the profile is built          | `pkg/sandbox/fence/seatbelt.go`          |
| Change the base rules of the profile     | `pkg/sandbox/fence/base.sb`              |
| Change the rules for the network         | `pkg/sandbox/fence/network.sb`           |
| Add an escape test                       | `pkg/sandbox/fence/seatbelt_test.go`     |

## Related Concepts

- [Sandbox](sandbox.md)
