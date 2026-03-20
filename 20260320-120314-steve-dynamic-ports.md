# Deep Review: 20260320-120314-steve-dynamic-ports

| | |
|---|---|
| **Date** | 2026-03-20 12:03 |
| **Branch** | `steve-dynamic-ports` |
| **Commits** | `1d65fa865` Use dynamic ports for Steve to avoid conflicts with other software<br>`8b3781948` Check Steve HTTP readiness instead of raw TCP connectivity |
| **Reviewers** | Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro |
| **Verdict** | **Merge with fixes** — two important issues around restart sequencing and proxy agent interference |
| **Wall-clock time** | 22 min 31 s |

---

## Consolidated Review

### Executive Summary

This change replaces Steve's hardcoded port 9443 with OS-assigned dynamic ports, propagates those ports to the Dashboard proxy and certificate-error handler, and upgrades the readiness check from a raw TCP connect to an HTTP probe. The implementation is well-structured with good comments and proper test coverage for the port allocation utility. Two important issues need attention: a restart race that can desynchronize the proxy from Steve, and the HTTP readiness probe routing through the app-wide proxy agent instead of connecting directly.

### Critical Issues

None.

---

### Important Issues

1. **Port desynchronization during restart** — `background.ts:1282-1290` / `steve.ts:46-52` [Codex GPT 5.4, Gemini 3.1 Pro] (important, regression)

```typescript
// background.ts:1282-1287
const [steveHttpsPort, steveHttpPort] = await getAvailablePorts(2);
console.log(`Steve ports: HTTPS=${ steveHttpsPort } HTTP=${ steveHttpPort }`);
DashboardServer.getInstance().setStevePort(steveHttpsPort);
setSteveCertPort(steveHttpsPort);
await Steve.getInstance().start(steveHttpsPort, steveHttpPort);
```

```typescript
// steve.ts:49-52
if (this.isRunning && pid) {
  console.debug(`Steve is already running with pid: ${ pid }`);
  return;
}
```

The orchestrator at `background.ts` lines 1285-1286 updates the Dashboard proxy and certificate-error handler to new ports before `Steve.start()` at line 1287 confirms whether it will actually launch a process. If Steve is still shutting down from a previous cycle (the `close` handler at `steve.ts:102` has not fired yet), `this.isRunning` remains `true` and `start()` returns early at line 52 — but the proxy already points to the new, unbound ports. The Dashboard loses connectivity until the next clean restart.

Before this change, the early return was harmless because the port was hardcoded to 9443. Dynamic ports make it dangerous: the published port and the actual port can diverge.

Codex rated this important; Gemini rated it critical. The timing window requires a rapid K8s restart cycle where `STARTED` fires before the old Steve process exits from `SIGINT`. In normal usage this window is narrow, so important is the appropriate severity.

Fix: Move the port publication (`setStevePort`, `setSteveCertPort`) after `Steve.start()` returns successfully. This guarantees the proxy and cert handler only see ports that Steve actually bound. The early-return path then becomes safe: if Steve is already running, nothing changes.

2. **HTTP readiness probe routes through proxy agent** — `steve.ts:151-157` [Codex GPT 5.4] (important, regression)

```typescript
const req = http.request({
  hostname: '127.0.0.1',
  port:     this.httpPort,
  path:     '/v1',
  method:   'GET',
  timeout:  1000,
}, (res) => {
```

`setupNetworking()` replaces `http.globalAgent` with an `ElectronProxyAgent` (at `networking/index.ts:51`) before Steve starts. The readiness check at `steve.ts:151` calls `http.request()` without an explicit `agent` option, so it inherits the proxy agent. In environments where PAC scripts or system proxy rules do not exempt `127.0.0.1`, the probe routes through the proxy instead of connecting directly. This can cause `waitForReady()` at line 122 to time out even though Steve is listening locally. The previous `net.Socket` approach bypassed the HTTP stack entirely and did not have this failure mode.

Fix: Add `agent: false` to the request options to force a direct connection:
```typescript
const req = http.request({
  hostname: '127.0.0.1',
  port:     this.httpPort,
  path:     '/v1',
  method:   'GET',
  timeout:  1000,
  agent:    false,
}, (res) => {
```

---

### Suggestions

1. **Comment references wrong function name** — `networking/index.ts:56` [Claude Opus 4.6] (suggestion, regression)

```typescript
// stevePort is 0 until setStevePort() is called, which is harmless:
```

The function defined at line 27 of this file is `setSteveCertPort`, not `setStevePort`. `setStevePort` is a method on `DashboardServer`.

Fix: Change `setStevePort()` to `setSteveCertPort()`.

2. **Proxy wrapper relies on async middleware returning truthy** — `dashboardServer/index.ts:103` [Claude Opus 4.6] (suggestion, regression)

```typescript
this.dashboardServer.use(key, (req, res, next) => this.proxies[key]?.(req, res, next) ?? next());
```

The `?? next()` fallback fires only when `this.proxies[key]` is absent (before `setStevePort`). It works because `createProxyMiddleware` v3 returns an async function — calling it yields a `Promise` (truthy), so the nullish coalescing skips `next()`. If a future version returned a synchronous `void`, `next()` would fire on every proxied request, racing the proxy response with the catch-all route at line 116. The existing comment at lines 96-101 explains why wrappers exist but not why `?? next()` is safe.

Fix: Add a one-line comment explaining the async invariant.

3. **HTTP readiness check accepts any response** — `steve.ts:157-159` [Claude Opus 4.6, Codex GPT 5.4] (suggestion, regression)

```typescript
}, (res) => {
  res.resume();
  resolve(true);
});
```

`isPortReady()` resolves `true` for any HTTP response, regardless of status code. Combined with the TOCTOU window in port allocation, another process that grabs the released port could satisfy the readiness check. In practice the risk is negligible because Steve binds within milliseconds, and the `/v1` path is unlikely to produce a `200` from an unrelated server.

Fix: Optionally check `res.statusCode < 500` before resolving `true`.

4. **Unhandled WebSocket upgrades leave socket open** — `dashboardServer/index.ts:143-146` [Gemini 3.1 Pro] (suggestion, gap)

```typescript
if (key) {
  return this.proxies[key]?.upgrade(req, socket, head);
}
console.log(`Unknown Web socket upgrade request for ${ req.url }`);
```

When the URL does not match any known proxy key, or when `this.proxies[key]` is absent, the code logs a message but leaves the socket open. The client hangs waiting for a `101 Switching Protocols` response. This behavior predates this change (confirmed by `git blame`: the log-and-return pattern at line 146 is from commit `d4b7af27` in 2022), but this change rewrote the URL matching logic and had an opportunity to fix it.

Fix: Add `socket.destroy()` after the log statement.

5. **WebSocket upgrade handler covers 4 of 10 proxy keys** — `dashboardServer/index.ts:133-141` [Claude Opus 4.6] (suggestion, gap)

```typescript
const key = req.url?.startsWith('/v1')
  ? '/v1'
  : req.url?.startsWith('/v3')
    ? '/v3'
    : req.url?.startsWith('/k8s/')
      ? '/k8s'
      : req.url?.startsWith('/api/')
        ? '/api'
        : undefined;
```

The upgrade handler dispatches WebSocket connections for `/v1`, `/v3`, `/k8s/`, and `/api/`, but `ProxyKeys` includes ten entries — several of which use `proxyWsOpts` (with `ws: false`, meaning manual upgrade handling is required). This is pre-existing behavior (the old if-else chain covered the same four paths), but this change could have made the handler data-driven from `ProxyKeys`.

Fix: Consider iterating `ProxyKeys` to find the matching prefix, eliminating the parallel if-chain.

---

### Design Observations

**Concerns**

- **Port state tracked in three places** (in-scope) — The Steve port is stored in `Steve.httpsPort`, `DashboardServer.stevePort`, and the `stevePort` module variable in `networking/index.ts`. The orchestrator in `background.ts` must update all three in the correct order. This triplication is the root cause of the restart desynchronization in Important Issue 1. A single "active Steve endpoint" owner — updated only after successful start and consumed by the proxy and cert handler — would eliminate the synchronization requirement. [Codex GPT 5.4, Gemini 3.1 Pro]

**Strengths**

- The proxy wrapper pattern at `dashboardServer/index.ts:102-103` solves the hot-swap problem: Express binds middleware at `init()` time, but the wrappers delegate to `this.proxies[key]` at call time. Replacing proxy instances on restart requires no route re-registration. [Claude Opus 4.6, Gemini 3.1 Pro]

- The `createProxies()` comment at `dashboardServer/index.ts:43-47` explains exactly why proxies must be recreated (the `onProxyReqWs` callback reads `options.target` for websocket path correction). This preserves critical institutional knowledge. [Gemini 3.1 Pro]

- `getAvailablePorts` binds all ports simultaneously before releasing any, guaranteeing distinct values. The TOCTOU race is honestly documented. [Claude Opus 4.6]

- Separating port allocation into the caller (`background.ts`) rather than inside `Steve.start()` keeps Steve a simple process wrapper and lets the caller coordinate with the proxy and cert handler. [Claude Opus 4.6]

---

### Testing Assessment

The `getAvailablePorts` tests in `networks.spec.ts` are thorough: they verify count, validity, distinct values, and actual bindability. The core orchestration lacks test coverage for the new dynamic port lifecycle.

Untested scenarios ranked by risk:

1. **Restart race**: `stop()` followed by immediate `start(newPort)` while the old process has not exited. Verifies the proxy does not end up pointing to an unbound port.
2. **Proxy agent interference**: Steve readiness check under a non-direct proxy configuration. The new HTTP probe routes through `http.globalAgent`.
3. **Port publication after failed start**: If `Steve.start()` throws after the proxy and cert handler have been updated, the Dashboard points to a dead port and the UI is still notified that Kubernetes is ready.
4. **`/api/steve-port` endpoint**: No test that the endpoint returns the correct port after `setStevePort`, or returns `0` before.
5. **`isPortReady` HTTP probe**: No unit test for the HTTP readiness check (the old TCP check also lacked tests).

---

### Documentation Assessment

The `dependencies.yaml` bump from `rancherDashboard: 2.11.1.rd3` to `2.11.1.rd4` is necessary — the patched Dashboard reads `/api/steve-port` to discover Steve's dynamic HTTPS port. No user-facing documentation changes are required since port assignment is an internal detail. The elimination of the 9443 port conflict is worth noting in release notes.

The internal contract change from fixed `9443` to the `/api/steve-port` endpoint should be documented wherever maintainers or downstream integrations assume Steve is always on `127.0.0.1:9443`.

---

## Agent Performance Retro

### Claude Opus 4.6

**Unique contributions**: Identified the `?? next()` async middleware coupling (Suggestion 2), the wrong function name in the comment (Suggestion 1), the behavioral change in error handling (Steve no longer swallows errors), and the WebSocket 4-of-10 proxy keys gap (Suggestion 5).

**Accuracy**: All findings verified. No false positives.

**Depth**: Did not run `git blame` (claimed to verify experimentally but did not show evidence of tracing the proxy agent path). Missed the two most important findings: the restart race and the proxy agent interference.

**Signal-to-noise**: Good — all findings are actionable, though none were high-severity.

### Codex GPT 5.4

**Unique contributions**: Identified the proxy agent interference with the readiness probe (Important 2). Also independently caught the restart race condition.

**Accuracy**: All findings verified. No false positives.

**Depth**: Strong — traced across `setupNetworking()`, `ElectronProxyAgent`, and `session.resolveProxy()` to identify the proxy agent issue. Also correctly identified the state triplication design concern.

**Signal-to-noise**: Excellent — every finding was actionable and substantive.

### Gemini 3.1 Pro

**Unique contributions**: Identified the socket.destroy() gap in the WebSocket upgrade handler (Suggestion 4).

**Accuracy**: One false positive — the mock backend's `desiredPort = 9443` at `mock.ts:192` is the Kubernetes API port (real backends default to 6443), not the Steve port. This file was not changed by this branch. The restart race was correctly identified but over-rated as critical.

**Depth**: Good analysis of the restart lifecycle and the `createProxies()` architecture. The socket.destroy() observation shows careful reading of the upgrade handler.

**Signal-to-noise**: Good overall, but the mock backend finding was noise.

### Summary Table

| Metric | Claude Opus 4.6 | Codex GPT 5.4 | Gemini 3.1 Pro |
|---|---|---|---|
| Duration | 6:40 | 4:44 | 12:02 |
| Critical | 0 | 0 | 1 (downgraded) |
| Important | 1 (downgraded) | 2 | 1 |
| Suggestion | 4 | 1 | 2 |
| Design observations | 4 strengths | 1 concern, 2 strengths | 1 concern, 2 strengths |
| False positives | 0 | 0 | 1 |
| Unique insights | 3 | 1 | 1 |

**Overall assessment**: Codex provided the most value — it found both important issues, had zero false positives, and finished fastest. Claude contributed the most unique findings but missed the two highest-severity issues. Gemini independently confirmed the restart race and added the socket.destroy() observation but was the slowest and produced a false positive.

---

## Appendix: Original Reviews

### Claude Opus 4.6

#### Executive Summary

This change eliminates the hardcoded Steve port 9443 in favor of OS-assigned dynamic ports, propagates those ports to the Dashboard proxy and certificate-error handler, and replaces the raw TCP readiness check with an HTTP-level probe. The implementation is clean and well-structured with good comments. There are no critical issues; the main actionable finding is a subtle correctness dependency in the proxy middleware wrapper that deserves a clarifying comment.

#### Findings

##### Critical Issues

None.

##### Important Issues

1. **Proxy `?? next()` correctness depends on http-proxy-middleware being async** — `dashboardServer/index.ts:103` (important, regression)

```typescript
this.dashboardServer.use(key, (req, res, next) => this.proxies[key]?.(req, res, next) ?? next());
```

The `?? next()` fallback is intended to call `next()` only when the proxy is absent (before `setStevePort`). It works correctly *because* `createProxyMiddleware` (v3.0.5) returns an `AsyncFunction` — calling it yields a `Promise`, not `undefined`, so nullish coalescing skips `next()`. I verified this experimentally: `createProxyMiddleware` returns a function with `constructor.name === 'AsyncFunction'`, and invocations return `Promise { undefined }`.

If http-proxy-middleware were updated to return a synchronous middleware (returning `void`/`undefined`), `undefined ?? next()` would fire for every proxied request, causing the catch-all route at line 116 to race the proxy and send `index.html` alongside the proxied response.

The existing comment (lines 96–101) explains *why* wrappers are used but not *why* `?? next()` is safe.

Fix: Add a brief comment explaining the async invariant, e.g.:
```typescript
// `?? next()` fires only when the proxy is absent: createProxyMiddleware
// returns an async function, so the call always yields a Promise (truthy).
```

##### Suggestions

2. **Comment references wrong function name** — `networking/index.ts:56` (suggestion, regression)

```typescript
// stevePort is 0 until setStevePort() is called, which is harmless:
```

The function in this file (line 27) is `setSteveCertPort`, not `setStevePort`. `setStevePort` is a method on `DashboardServer`.

Fix: Change to `setSteveCertPort()`.

3. **HTTP readiness check treats any response as "ready"** — `steve.ts:149-159` (suggestion, regression)

```typescript
}, (res) => {
  res.resume();
  resolve(true);
});
```

The callback at line 157 resolves to `true` regardless of the HTTP status code. If Steve's HTTP listener starts before registering the `/v1` route handler, a 404 from the default handler would be treated as "ready." The comment at line 144–147 explains the motivation (TCP connect is insufficient), and in practice Steve likely registers routes before listening, so this is probably fine. But checking for a non-5xx status would add a small safety margin.

Fix: Optionally check `res.statusCode < 500` before resolving `true`.

4. **Steve `start()` error handling behavior changed** — `steve.ts:105-116` vs old `steve.ts:96-111` (suggestion, regression)

The old `start()` had an internal try/catch (old lines 96–111) that swallowed spawn and `waitForReady` errors — callers never knew Steve failed. The new code removes that try/catch, so errors now propagate. The caller in `background.ts:1281–1290` has a proper try/catch, so this is functionally correct and arguably better (the old behavior was a silent failure). Worth noting as a deliberate behavioral change for reviewers, since it's invisible from the diff alone.

5. **WebSocket upgrade handler covers 4 of 10 ProxyKeys** — `dashboardServer/index.ts:133-141` (suggestion, gap)

```typescript
const key = req.url?.startsWith('/v1')
  ? '/v1'
  : req.url?.startsWith('/v3')
    ? '/v3'
    : req.url?.startsWith('/k8s/')
      ? '/k8s'
      : req.url?.startsWith('/api/')
        ? '/api'
        : undefined;
```

The upgrade handler dispatches WebSocket upgrades for `/v1`, `/v3`, `/k8s/`, and `/api/`, but ProxyKeys also includes `/pp`, `/apis`, `/v3-public`, `/api-ui`, `/meta`, and `/v1-*etc` — several of which use `proxyWsOpts` (which sets `ws: false`, meaning upgrades must be handled manually). This is pre-existing behavior (the old code had the same four paths), but this change had an opportunity to make the upgrade handler data-driven from `ProxyKeys` instead of maintaining a parallel if-chain.

#### Design Observations

**Strengths**

- The wrapper-function approach in `dashboardServer/index.ts:102–103` elegantly solves the hot-swap problem: Express middleware references are bound once at `init()` time, but the wrappers delegate to `this.proxies[key]` at call time, so `createProxies()` can replace the proxy instances without re-registering routes. This is a clean solution to a real constraint (Express doesn't support removing middleware).

- The `getAvailablePorts` utility binds all ports simultaneously before releasing any, guaranteeing distinct values. The TOCTOU race is honestly documented (the comment at `networks.ts:14–16` even adds `TOCTOU` to the spell-check dictionary). The risk is negligible in practice.

- The `/api/steve-port` endpoint registered before the proxy routes (line 89–94) is a simple, pragmatic way for the Dashboard to discover Steve's dynamic port. The comment explaining route ordering is helpful.

- Separating the port allocation into the caller (`background.ts`) rather than inside `Steve.start()` keeps Steve as a simple process wrapper and lets the caller coordinate port assignment with the proxy and cert handler. Good separation of concerns.

#### Testing Assessment

The `getAvailablePorts` tests in `networks.spec.ts` are well-structured — they verify count, validity, distinct values, and actual bindability. Untested scenarios, ranked by risk:

1. **Steve restart lifecycle**: No test that `setStevePort` → `createProxies()` correctly replaces proxy targets. The middleware wrapper indirection makes this hard to unit-test but it's the core mechanism of the change.
2. **`/api/steve-port` endpoint**: No test that the endpoint returns the correct port after `setStevePort` is called, or that it returns `0` before.
3. **`isPortReady` HTTP probe**: No unit test for the new HTTP readiness check (the old TCP check also lacked tests, so this is pre-existing).
4. **Error path — Steve fails to start**: No test that the caller's try/catch correctly handles Steve.start() failure and that the UI state notification still fires.

#### Documentation Assessment

The `dependencies.yaml` bump from `rancherDashboard: 2.11.1.rd3` to `2.11.1.rd4` is necessary — the patched Dashboard must know to read `/api/steve-port` to discover Steve's dynamic port. No additional documentation changes appear needed since this is internal infrastructure invisible to end users.

---

### Codex GPT 5.4

#### Executive Summary

Verdict: Needs changes.

This branch correctly moves Steve off the fixed `9443` port and improves readiness checking beyond a raw TCP connect. I found two important regressions: one in restart sequencing that can publish a new port before a new Steve process actually exists, and one in the new HTTP readiness probe inheriting the app-wide proxy agent, which can break startup in proxied environments.

#### Critical Issues

None.

#### Important Issues

1. **Port state is published before a new Steve instance is confirmed** — `background.ts:1285` / `steve.ts:49` / `background.ts:1299` (important, regression)

```ts
DashboardServer.getInstance().setStevePort(steveHttpsPort);
setSteveCertPort(steveHttpsPort);
await Steve.getInstance().start(steveHttpsPort, steveHttpPort);

if (this.isRunning && pid) {
  return;
}
```

The new code switches the dashboard proxy and certificate allow-list to `steveHttpsPort` at `background.ts` lines 1285-1286 before `Steve.start()` at line 1287 has proved it can launch a fresh process. That matters because `Steve.stop()` is still fire-and-forget at `background.ts` line 1299 and `steve.ts` lines 174-179, while `Steve.start()` still exits early when `this.isRunning && pid` at `steve.ts` lines 49-52. On a restart where the old Steve process has not finished exiting yet, the dashboard and cert handler move to the new port, but Steve keeps serving the old one. The result is a broken dashboard reconnect path during restart even though the old Steve may still be alive.

Fix: do not publish the new port until `Steve.start()` has actually spawned the replacement process, or make `stop()` await process exit before allocating and publishing new ports. A clean shape would be for `Steve.start()` to own the port handoff and return the active ports only after launch succeeds.

2. **The new readiness probe now depends on proxy configuration** — `steve.ts:151` / `main/networking/index.ts:51` / `main/networking/proxy.ts:20` (important, regression)

```ts
http.globalAgent = proxyAgent;

const req = http.request({
  hostname: '127.0.0.1',
  port:     this.httpPort,
  path:     '/v1',
```

`setupNetworking()` installs `ElectronProxyAgent` as `http.globalAgent` at `main/networking/index.ts` lines 46-52 before Steve startup begins, and that agent resolves every URL via `session.resolveProxy()` at `main/networking/proxy.ts` lines 20-32. The new readiness check in `steve.ts` lines 151-157 uses the default agent, so it no longer guarantees a direct loopback connection. In environments with PAC or proxy rules that do not exempt `127.0.0.1`, `waitForReady()` at `steve.ts` lines 115-140 can time out even when Steve is already listening locally. The previous `net.Socket` connect did not have this failure mode.

Fix: force a direct local probe by passing `agent: false`, a plain non-proxy `http.Agent`, or by keeping the readiness check on an explicit `net.connect()` while separately validating HTTP semantics.

#### Suggestions

1. **Readiness should verify Steve, not just "some HTTP server"** — `steve.ts:151` / `utils/networks.ts:13` (suggestion, regression)

```ts
path: '/v1',
}, (res) => {
  res.resume();
  resolve(true);
});
```

`isPortReady()` at `steve.ts` lines 151-159 treats any HTTP response as success. Combined with the acknowledged TOCTOU window in `utils/networks.ts` lines 13-15 and the port release at lines 32-34, a different local process that grabs the selected port can satisfy readiness even though Steve never bound it.

Fix: check for a Steve-specific status/header/body, or use a dedicated health endpoint whose response uniquely identifies Steve.

#### Design Observations

**Concerns**
- `(in-scope)` The Steve endpoint is now tracked in three places: `background.ts`, `DashboardServer`, and the networking certificate handler. That duplication is what enabled the restart desynchronization above. A single "active Steve endpoint" owner, updated only after successful start and consumed by the proxy and cert handler, would remove an entire class of state-sync bugs.

**Strengths**
- Recreating proxy middleware instead of using a dynamic router is the right call here; the comment in `dashboardServer/index.ts:42` correctly preserves the websocket path-fixup behavior in `onProxyReqWs`.
- Switching readiness from raw TCP to an actual HTTP request is directionally correct; it matches the stated problem that Steve can accept connections before its API controllers are ready.

#### Testing Assessment

1. No test covers a restart where `Steve.stop()` has been requested but the old process is still running when the next `STARTED` event publishes fresh ports.
2. No test covers Steve readiness under a non-direct proxy / PAC configuration, even though the new probe now routes through `http.globalAgent`.
3. No test covers startup failure after port publication, to verify the dashboard is not pointed at a dead port and the UI is not enabled incorrectly.
4. No test covers the `/api/steve-port` contract end-to-end with the updated dashboard dependency.

#### Documentation Assessment

The internal contract change from fixed `9443` to `/api/steve-port` should be documented anywhere maintainers or downstream integrations assume Steve is always on `127.0.0.1:9443`. I did not find repo-local documentation or tests that describe that new contract.

---

### Gemini 3.1 Pro

#### Executive Summary

An excellent and highly beneficial change that resolves a common pain point (port conflicts on 9443) while improving the robustness of the readiness check. The overall approach is solid, but there is a critical race condition during rapid restarts where the orchestration layer updates the dashboard proxy to new ports, but the Steve process ignores the start request and continues running on the old ports.

#### Critical Issues

1. **Port desynchronization during rapid restarts** — `pkg/rancher-desktop/backend/steve.ts:44-55` (critical, regression)

```typescript
  public async start(httpsPort: number, httpPort: number) {
    const { pid } = this.process || { };

    if (this.isRunning && pid) {
      console.debug(`Steve API is already running with pid: ${ pid }`);

      return;
    }

    this.httpsPort = httpsPort;
    this.httpPort = httpPort;
```

When Kubernetes restarts, `background.ts` unconditionally allocates two new random ports, updates the Dashboard proxy routing, and then calls `Steve.getInstance().start(newHttps, newHttp)`.

If Kubernetes restarts quickly (e.g. changing K8s state before the old Steve process has finished gracefully shutting down from `SIGINT`), `this.isRunning` will still evaluate to `true`. When this happens, `start()` returns immediately without applying the new ports or restarting the process. However, the Dashboard proxy has *already* been repointed to the newly allocated (and unbound) ports. This permanently breaks the Dashboard UI until the next full backend restart.

Fix: Make Steve the single source of truth for its own ports. Instead of `background.ts` passing ports down, `steve.ts` should allocate its own ports when launching a new process and expose a `getPorts()` method. Alternatively, if `start()` detects it is already running, it should wait for the pending exit to complete before spawning a new process on the newly requested ports.

#### Important Issues

1. **Unhandled WebSocket upgrades hang the client** — `pkg/rancher-desktop/main/dashboardServer/index.ts:133-147` (important, gap)

```typescript
        if (key) {
          return this.proxies[key]?.upgrade(req, socket, head);
        }
        console.log(`Unknown Web socket upgrade request for ${ req.url }`);
```

If a WebSocket upgrade request arrives before `createProxies()` is called, or if the path evaluates to an unknown `key`, the code logs a message but leaves the `socket` untouched. Because the socket is neither upgraded nor explicitly destroyed, the client connection will hang indefinitely waiting for an HTTP 101 Switching Protocols response.

Fix: Destroy the socket if the upgrade cannot be handled.
```typescript
        if (key) {
          const proxy = this.proxies[key];
          if (proxy) return proxy.upgrade(req, socket, head);
        }
        console.log(`Unknown or unready Web socket upgrade request for ${ req.url }`);
        socket.destroy();
```

#### Suggestions

1. **Stale error listener on successful port allocation** — `pkg/rancher-desktop/utils/networks.ts:19-22` (suggestion, enhancement)

```typescript
      await new Promise<void>((resolve, reject) => {
        server.once('error', reject);
        server.listen(0, '127.0.0.1', resolve);
      });
```

When `server.listen()` succeeds, the Promise resolves, but the `reject` listener remains attached to the `server`'s `error` event. Since the server is closed and discarded immediately afterward, this does not cause a practical memory leak, but it is a minor anti-pattern.
Fix: Consider removing the error listener upon successful binding: `server.listen(0, '127.0.0.1', () => { server.removeListener('error', reject); resolve(); });`

2. **Mock backend retains hardcoded 9443 port** — `pkg/rancher-desktop/backend/mock.ts:192` (suggestion, gap)

The mock backend uses `9443` as its `desiredPort`. `desiredPort` typically represents the Kubernetes API port (which defaults to 6443 in the other backend classes), not the Steve API port.
Fix: Change `desiredPort = 9443;` to `desiredPort = 6443;` for consistency with the rest of the codebase.

*Consolidation note: This finding is a false positive with respect to this change. `desiredPort` is the Kubernetes API port, not the Steve port. The real backends default to 6443. The mock's 9443 is pre-existing and unrelated to this branch, which does not modify `mock.ts`.*

#### Design Observations

**Concerns**
- **Port Allocation Responsibility (in-scope)**: Currently, `background.ts` acts as an orchestrator that blindly fetches random ports and pushes them to both `steve.ts` and the `DashboardServer`. This leaks low-level socket allocation into the high-level orchestrator and is the root cause of the synchronization bug identified above. A more resilient design would encapsulate port allocation entirely within `steve.ts`. The orchestrator could simply call `await Steve.getInstance().start()` and then retrieve the definitively active ports via a getter to configure the proxy.

**Strengths**
- The comprehensive comment in `createProxies()` explaining exactly *why* the proxy middlewares must be completely recreated on port changes (due to `proxyUtils` reading `options.target`) is excellent. It preserves vital institutional knowledge and prevents future regressions.
- Upgrading the readiness check in `isPortReady()` from a raw TCP connect to a valid HTTP request ensures the server is actively routing, circumventing the edge case where Steve binds the port before its internal API controllers are fully initialized. Calling `res.resume()` to safely consume and discard the payload is exactly the right pattern to prevent stream leaks.

#### Testing Assessment

The new port allocation utility is well-tested in `networks.spec.ts`. However, the core backend orchestration lacks coverage for the new dynamic port lifecycles.

Untested scenarios (ranked by risk):
1. **Rapid Restart Cycles:** Triggering `stop()` immediately followed by `start(newPort)` on the `Steve` singleton to ensure the internal state (`isRunning`, `process.kill()`) transitions safely without leaving the dashboard proxy pointing to a dead port.
2. **Missing Proxy Handlers:** Sending an HTTP WebSocket upgrade request to `DashboardServer` before `setStevePort` has been initialized to verify the connection is cleanly rejected rather than held open.

#### Documentation Assessment

The inline code documentation accurately captures the architectural constraints (e.g., explaining the TOCTOU race for port binding). No user-facing documentation updates are strictly required since port assignment is an internal detail, though the elimination of the `9443` port conflict is worth highlighting in user-facing release notes.
