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
4. Create shares with `Bridge.createShare(backendMode, shareMode, target)`.
   Treat the returned share token as a secret. Persist it only in app-private storage.
5. For a TCP share, call `Bridge.startTCPForwarder(shareToken, localAddress)`.
   Call `Bridge.stopTCPForwarder(shareToken)` before deleting the share.
6. To sign out, stop every forwarder, then call `Bridge.disableEnvironment()`.
   It asks the controller to disable the environment before deleting the local Ziti
   identity and credential-bearing environment file. If the remote disable fails,
   local credentials are retained so the user can retry.

## Safety and scope

- The bridge refuses to disable an environment while TCP forwarders are active.
- It refuses to switch storage roots while forwarders are active.
- The caller must collect account tokens through a secure UI and must not commit
  real credentials or `.zrok2` data to Git.
- The smoke test and CI build do not authenticate to a controller or prove a live
  tunnel. Real account authentication and end-to-end forwarding still require a
  separately authorized runtime test.
- This prototype is separate from the JENEW Android application and does not
  modify its MQTT broker or lifecycle.
