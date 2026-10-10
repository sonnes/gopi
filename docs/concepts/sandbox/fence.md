---
title: Fence
description: How a command runs inside an OS sandbox that limits its writes, its reads, its network, and its environment.
package: pkg/sandbox/fence
read_when:
  - You confine the commands that a model runs on this host
  - A command fails with "Operation not permitted" inside a fence
  - You add a backend for another OS
---

# Fence

A fence runs a command inside an OS sandbox. The command and its children can write only to the write directories. They cannot read the hidden paths, and they have no network unless the policy turns it on. On macOS, the backend is Seatbelt: the fence runs each command through `sandbox-exec` with a generated profile.

```
  PROFILE, in order. A later rule wins.
  +--------------------------------------------------------------+
  | base.sb        deny default; allow exec, fork, a few sysctls |
  | allow          file-read* everywhere                         |
  | preferences.sb reads of the user preferences                 |
  | allow          file-write* in each Write directory           |
  | network.sb     only with Network(true)                       |
  | deny           XPC services, two fcntls; allow /dev/fd       |
  | deny           writes to ReadOnly paths, renames of parents  |
  | deny           writes to ReadOnlyNames, renames of their dirs|
  | deny           reads of HideExcept, except the kept paths    |
  | deny           reads and writes of Hide paths                |
  +--------------------------------------------------------------+
```

## Design

- The profile denies by default. An allow-default profile blocks writes but leaves system services open, and a service such as LaunchServices starts a process outside the sandbox. The base rules come from the [Seatbelt policy of OpenAI Codex](https://github.com/openai/codex/blob/696b4502dfaacd91133c3691a23dc91ae3b14bc9/codex-rs/sandboxing/src/seatbelt_base_policy.sbpl), under the Apache License 2.0.
- A fenced command and its children write only inside the write directories, and a symlink in a write directory does not lead a write outside. A program outside the fence that later runs a file from a write directory runs it without limits. So `Workspace` keeps each file that git, a shell, or an editor runs read-only, at the root and in each directory under it. The list comes from [sandbox-runtime](https://github.com/anthropics/sandbox-runtime/blob/22b545aa6661491bcb911e7239a8d29b17de0bae/src/sandbox/sandbox-utils.ts), with `.git/commondir` added. The cost: `git init` and `git remote add` fail inside the fence.
- A read-only path also protects each parent that a command can rename, up to the outermost write directory. Otherwise, a rename of a root in the temp directory moves `.git/hooks` out of the rule.
- The constructor resolves each path of the policy to its real path, because Seatbelt matches the real path of a file. So `/var/folders/...` becomes `/private/var/folders/...`. A path that becomes a symlink after the constructor runs is not in the policy.
- A command can read every path outside the home directory, because a read allowlist breaks compilers and interpreters, which read from all over the disk. `Workspace` hides the home directory except the root and the tool directories, because it holds the histories and the credentials of the user. The cost: a tool that reads another file there fails, and a `.env` file in the project stays readable.
- `Workspace` puts the build caches in the temp directory, because a program outside the fence reads the cache directory of the user.
- With the network on, a command cannot connect to a unix socket, except the resolver of macOS. A socket reaches a process outside the fence, such as Docker.
- On an OS without a backend, `New` returns an error. The constructor runs `/usr/bin/true` in the fence and returns an error if it fails. So a caller cannot run a command outside the sandbox by mistake.
- With `Env`, the command gets only the named variables, and `Workspace` names a short list. A profile that the command loads can export a secret again.

## Map

| Task                                     | File                                     |
| ---------------------------------------- | ---------------------------------------- |
| Change the options or the Workspace policy | `pkg/sandbox/fence/fence.go`           |
| Change how the profile is built          | `pkg/sandbox/fence/seatbelt.go`          |
| Change the base rules of the profile     | `pkg/sandbox/fence/base.sb`              |
| Change the rules for the network         | `pkg/sandbox/fence/network.sb`           |
| Add an escape test                       | `pkg/sandbox/fence/seatbelt_test.go`     |

## Related Concepts

- [Sandbox](sandbox.md)
