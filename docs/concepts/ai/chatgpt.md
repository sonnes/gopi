---
title: ChatGPT Plan
description: How gopi signs in with a ChatGPT account, keeps its rotating tokens, and calls the Responses API on the plan of the user.
package: pkg/ai/chatgpt, pkg/ai/oauth
read_when:
  - You change the sign-in or the sign-out of gopi login
  - A call on the plan fails with a 400 or a 401
  - Two gopi processes share one login file
  - You add a provider with an OAuth sign-in
---

# ChatGPT Plan

The `chatgpt` provider runs a model call on the ChatGPT plan of a user, with no API key. It signs in with Sign in with ChatGPT, keeps the account in a login file, and sends each call through the `openai` provider with `openai.ChatGPTPlan`. The call goes to the plan route: the Responses API of api.openai.com, with an OAuth token of the plan.

```mermaid
flowchart LR
  accTitle: The parts of a call on the ChatGPT plan
  accDescr: gopi login writes the account to the login file. A call goes from the chatgpt provider through the openai provider and the OAuth transport to the plan route. The transport gets its token from the file source, which refreshes the token under a lock on the login file.
  login([gopi login chatgpt]) -- SignIn --> file[(auth.json)]
  call([model call]) --> cg[chatgpt.Provider]
  cg --> oa[openai.Provider with ChatGPTPlan]
  oa --> tr[oauth.Transport]
  tr -- Token --> src[FileSource]
  src -- lock, read, refresh, write --> file
  tr --> route[plan route: POST /v1/responses]
```

## Design

- The first sign-in registers gopi with the client ID `dynamic_agent_client`, and keeps the client ID that OpenAI issues. Each later sign-in, and each sign-out, keeps that registration. A sign-in without it adds a new registration to the account of the user, and each registration stays until the user removes it.
- The loopback listener binds 127.0.0.1, not localhost, because OpenAI refuses localhost in a redirect URI. Only the port of the redirect URI can change between sign-ins, so `gopi login` uses one port.
- A sign-in checks the RS256 signature of the ID token against the keys of OpenAI, then the issuer, the audience, the expiry, and the nonce. OpenAI signs only with RS256, so `crypto/rsa` verifies the signature, and the module has no JOSE dependency.
- A sign-in fails if the user did not grant the plan scope. An identity alone cannot call the plan route.
- The refresh is in the source of the token, not in the transport. The transport asks for a token before each request and keeps none, so one source can serve many providers.
- The refresh token rotates on each refresh, so two processes that send the same refresh token lose one sign-in. `FileSource` takes a lock on the login file, reads the file again, and refreshes only if the token in the file is still not valid. On a system without flock, the lock holds for one process only.
- `openai.ChatGPTPlan` removes the request fields that the plan route refuses, puts the function tools in one namespace, and streams an object call. The route refuses a function tool outside a namespace and a call that does not stream. [OpenAI Providers](openai.md) has the stream rules.
- gopi has no sign-in for a Claude plan. Anthropic does not let another app offer a Claude.ai login or send calls with the credentials of a plan. A Claude plan runs through the claude-code driver, and the API credits of a plan work through an API key.
- The CLI puts the detector of the login before the detectors of package env. A sign-in is a choice of the user, so the default model of gopi then comes from the plan.

## Map

| Task                                         | File                          |
| -------------------------------------------- | ----------------------------- |
| Change the sign-in, the refresh, or sign-out | `pkg/ai/chatgpt/auth.go`      |
| Change the check of the ID token             | `pkg/ai/chatgpt/idtoken.go`   |
| Change the login file or its lock            | `pkg/ai/chatgpt/store.go`     |
| Change how a model of the plan lists         | `pkg/ai/chatgpt/chatgpt.go`   |
| Change what a request on the plan sends      | `pkg/ai/openai/plan.go`       |
| Change the loopback listener or PKCE         | `pkg/ai/oauth/callback.go`, `pkg/ai/oauth/oauth.go` |
| Change gopi login or gopi logout             | `cmd/gopi/login.go`           |

## Related Concepts

- [OpenAI Providers](openai.md)
- [Host Provider](host-provider.md)
- [Provider](provider.md)
- [Catalog](../catalog/catalog.md)
