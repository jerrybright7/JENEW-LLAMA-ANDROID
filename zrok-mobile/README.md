# JENEW zrok Mobile Bridge

This module wraps the zrok v2.0.9 Go SDK for Android through gomobile.

## Environment lifecycle

1. Call `Bridge.initialize(<absolute app-private directory>)`, normally using Android's
   `Context.getFilesDir()/zrok2`. Do not use shared or external storage.
2. Call `Bridge.enableEnvironment(apiEndpoint, accountToken, host, description)`.
   The account token is written only to zrok's environment file beneath that private
   directory. Never pass it through logs, analytics, intents, URLs, or source control.
3. Inspect `Bridge.status()`. It returns non-secret flags and the configured API
   endpoint; it never returns the account token, identity contents, or storage path.
4. Create as many independent shares as needed with
   `Bridge.createShare(backendMode, shareMode, target)`. Each share has its own token,
   frontend endpoint(s), backend target, and lifecycle. The bridge records every
   created share in `managed-shares.json` under the app-private zrok directory.
5. Call `Bridge.listManagedShares()` to retrieve the locally managed service list as
   JSON. It includes each share token for follow-up bridge calls, so never log, export,
   or expose this response. The registry file is written with owner-only permissions.
6. For each TCP share, call `Bridge.startTCPForwarder(shareToken, localAddress)`.
   Multiple tokens can have independent listeners concurrently. Call
   `Bridge.stopTCPForwarder(shareToken)` before deleting that share. After a process
   restart, saved shares remain listed but are correctly marked stopped until started
   again; live listeners are not silently claimed to have survived process death.
7. To sign out, stop every forwarder, then call `Bridge.disableEnvironment()`.
   It asks the controller to disable the environment before deleting the local Ziti
   identity and credential-bearing environment file. If the remote disable fails,
   local credentials are retained so the user can retry.

## Safety and scope

- The service registry is local to the app-private directory; it is not a substitute
  for querying the zrok controller for shares created outside this bridge.
- The bridge refuses to disable an environment while TCP forwarders are active.
- It refuses to switch storage roots while forwarders are active.
- The caller must collect account tokens through a secure UI and must not commit
  real credentials or `.zrok2` data to Git.
- The smoke test and CI build do not authenticate to a controller or prove a live
  tunnel. Real account authentication and end-to-end forwarding still require a
  separately authorized runtime test.
- This prototype is separate from the JENEW Android application and does not
  modify its MQTT broker or lifecycle.


## Expanded mobile API surface

The bridge also exposes advanced share creation through
`Bridge.createShareWithOptions(optionsJSON)`. Its JSON options map to the zrok
SDK share request, including Basic Auth, OAuth provider/domain restrictions,
permission mode, access grants, private-share token selection, and reserved
namespace/name selections. The JSON interface is intentional: it avoids binding
Go request structs through gomobile.

Private access grants can be created, listed, and revoked with
`Bridge.createAccessGrant(shareToken, bindAddress)`,
`Bridge.listManagedAccesses()`, and
`Bridge.deleteAccessGrant(accessToken)`. The access registry is stored with
owner-only permissions. Access tokens and share tokens are secrets: never log
these API results or send them to analytics.

`Bridge.getShareDetail(shareToken)` retrieves controller-side share metadata.
These additions provide access and share-management primitives; they do not by
themselves claim that every backend forwarding engine or Android background
service is implemented. Backend runtime support must be verified independently.
