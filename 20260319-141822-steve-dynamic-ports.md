# Deep Review: 20260319-141822-steve-dynamic-ports

| | |
|---|---|
| **Date** | 2026-03-19 14:18 |
| **Branch** | `steve-dynamic-ports` |
| **Commits** | `bf5389558` Use dynamic ports for Steve to avoid conflicts with other software |
| **Reviewers** | Claude Opus 4.6, Codex GPT 5.4, Gemini 3.1 Pro |
| **Verdict** | **Merge with fixes** — address the duplicate-port risk and silent startup failure; the proxy-not-ready window is cosmetic |
| **Wall-clock time** | 14 min 49 s |

---

## Consolidated Review

### Executive Summary

This change replaces Steve's hardcoded port 9443 with dynamically allocated ports, eliminating conflicts with other software. Ports are allocated in `background.ts`, then propagated to three consumers: Steve itself, the dashboard proxy, and the certificate-error handler. The dashboard proxy uses a wrapper pattern so express routes survive proxy recreation across restarts. The approach is sound, with two gaps worth fixing before merge: the two sequential `getAvailablePort()` calls can theoretically return the same port, and Steve's `start()` swallows bind failures instead of propagating them.

### Critical Issues

None.

### Important Issues

1. **Sequential port allocation can return identical ports** — `background.ts:1281-1282` [Codex GPT 5.4, Gemini 3.1 Pro]

```ts
const steveHttpsPort = await getAvailablePort();
const steveHttpPort = await getAvailablePort();
```

`getAvailablePort()` at `networks.ts:16-24` binds port 0, reads the assigned port, then closes the server before returning. Because the port leaves no `TIME_WAIT` state, the OS may reassign it on the next `listen(0)` call. If both calls return the same port, Steve fails to bind its second listener. The probability is low on macOS and Linux (their ephemeral allocators use incrementing counters), but higher on Windows.

Fix: Allocate both ports simultaneously — bind two servers on port 0 before closing either — to guarantee distinct ports.

2. **Steve startup failure is swallowed** — `steve.ts:116-118` [Gemini 3.1 Pro]

```ts
    } catch (ex) {
      console.error(ex);
    }
```

If Steve fails to bind its ports (e.g., from the duplicate-port scenario above, or a TOCTOU collision), `waitForReady()` at line 115 throws. The `catch` block logs the error but does not rethrow, so `start()` returns as if Steve started successfully. The caller in `background.ts:1287` then proceeds to notify the UI that Kubernetes is ready (line 1293), leaving the dashboard proxy pointed at a dead port.

Per `git blame`, this error swallowing predates this change (commit `b2e1cc0b70`), so it is a gap rather than a regression. However, dynamic ports increase the chance of bind failure, making the gap more consequential.

Fix: Rethrow the error from `start()` so `background.ts` can handle it — either by retrying with fresh ports or by reporting the failure to the user.

3. **`/api/steve-port` endpoint has no documented consumer** — `dashboardServer/index.ts:91-93` [Claude Opus 4.6]

```ts
this.dashboardServer.get('/api/steve-port', (_req, res) => {
  res.json({ port: this.stevePort });
});
```

This endpoint is new in this change, but nothing in the Rancher Desktop source fetches it. The simultaneous bump from `rancherDashboard 2.11.1.rd3` to `2.11.1.rd4` in `dependencies.yaml` suggests the consumer lives in the dashboard build artifact. If so, the contract (`GET /api/steve-port` → `{ port: number }`) should be documented to prevent accidental breakage. If no consumer exists yet, remove the endpoint until one does.

Fix: Add a comment or brief doc noting that Rancher Dashboard rd4+ fetches this endpoint to discover Steve's dynamic port.

### Suggestions

1. **Websocket upgrade silently drops when proxies are uninitialized** — `dashboardServer/index.ts:139-141` [Claude Opus 4.6, Gemini 3.1 Pro]

```ts
if (key) {
  return this.proxies[key]?.upgrade(req, socket, head);
}
```

Before this change, `this.proxies` was populated at construction via an IIFE, so `.upgrade()` was always callable. Now `this.proxies` starts empty (`Object.create(null)` at line 30) and is populated only when `setStevePort()` is called. If a websocket upgrade arrives before that, the optional chain evaluates to `undefined` and the socket is never closed or logged.

In practice this is unreachable: the dashboard button is disabled until Kubernetes reaches STARTED state (`DashboardOpen.vue:17`), and `setStevePort()` is called before the UI notification at `background.ts:1285-1293`. Gemini rated this CRITICAL, but since the UI prevents the scenario, it is a defensive improvement rather than a practical bug.

Fix: Destroy the socket and log when the proxy is unavailable:
```ts
if (key) {
  if (this.proxies[key]) {
    return this.proxies[key].upgrade(req, socket, head);
  }
  console.log(`Proxy not ready for websocket upgrade to ${ req.url }`);
  socket.destroy();
  return;
}
```

2. **Distinct-ports test asserts unguaranteed OS behavior** — `networks.spec.ts:28-33` [Codex GPT 5.4]

```ts
it('returns distinct ports on consecutive calls', async() => {
  const port1 = await getAvailablePort();
  const port2 = await getAvailablePort();

  expect(port1).not.toBe(port2);
});
```

`getAvailablePort()` closes its socket before returning, so two consecutive calls may legally return the same ephemeral port on some kernels. This makes the test flaky without validating the actual product requirement (that Steve gets two distinct ports).

Fix: If finding #1 is addressed by allocating both ports simultaneously, this test becomes moot. Otherwise, drop the assertion or replace it with a test that binds both ports concurrently.

---

## Design Observations

1. **Wrapper function pattern for dynamic proxy targets is well-chosen** (in-scope)

The approach of registering wrapper functions in express that dereference `this.proxies[key]` at call time (line 99) handles proxy recreation across Steve restarts without re-registering express routes. The comment at lines 95-97 explains the rationale. This is a clean solution.

2. **Steve could own port selection to eliminate TOCTOU entirely** (future)

Passing `0` to Steve for its listen ports and having it report the bound ports back (via stdout or a Unix socket) would remove the duplicated port state spread across `background.ts`, `dashboardServer`, and `main/networking`. It would eliminate both the TOCTOU allocation race and the duplicate-port risk, because the app would only publish ports Steve has already bound. This would require changes to the Steve Go binary.

3. **Three-way port propagation is manageable now but fragile at scale** (future)

The Steve HTTPS port fans out to three consumers from `background.ts:1281-1287`. If future changes add more consumers, a lightweight event (`mainEvents.emit('steve-port-changed', port)`) would let consumers self-register. The current three-consumer count does not warrant the abstraction yet.

---

## Testing Assessment

The new `networks.spec.ts` covers `getAvailablePort()` with three meaningful tests: port is positive, port is bindable, and (aspirationally) consecutive calls return distinct values.

Untested scenarios, ranked by risk:

1. **Steve restart cycle** — allocate new ports, recreate proxies, start Steve, verify proxy routes to new port. This exercises the most complex interaction but requires mocking Electron and the backend state machine.
2. **Bind failure recovery** — Steve fails to start because allocated ports were claimed by another process. No test verifies that the error propagates to the caller or that the system retries.
3. **Identical-port allocation** — No test covers the case where both `getAvailablePort()` calls return the same value.
4. **`/api/steve-port` response contract** — If the dashboard consumes this endpoint, its response format (`{ port: number }`) should be tested to prevent accidental breakage.

---

## Documentation Assessment

The TOCTOU risk in `getAvailablePort()` is documented in the code comment at `networks.ts:10-14`. The `TOCTOU` spelling addition to `expect.txt` is appropriate. The new `/api/steve-port` endpoint lacks documentation of its consumer and contract — see finding #3.

---

## Agent Performance Retro

### Claude Opus 4.6

Traced deeply into the UI layer (`DashboardOpen.vue:17`) to confirm the proxy-not-ready scenario is unreachable, correctly downgrading Gemini's CRITICAL to a suggestion. Identified the undocumented `/api/steve-port` endpoint as potentially dead code. Noted the `desiredPort = 9443` in `backend/mock.ts` as a coincidental value. Provided three nuanced design observations. Did not flag the duplicate-port or error-swallowing issues.

### Codex GPT 5.4

Identified both the duplicate-port risk and the restart ordering concern. Raised the flaky test assertion. Proposed the strongest design observation (have Steve own port selection). The restart ordering finding (`background.ts` publishes ports before Steve binds them) initially sounds alarming but is less severe on closer inspection: `Steve.start()` awaits `waitForReady()`, so the proxies are updated before Steve is ready but the UI notification at line 1293 follows `await Steve.getInstance().start()`. The real window is only the brief period during `waitForReady()` polling, which is acceptable.

### Gemini 3.1 Pro

Rated the proxy-not-ready window as CRITICAL, which overstates its practical severity — Claude's trace through the UI showed the scenario is unreachable during normal operation. Correctly identified the silent startup failure gap with a concrete fix. Proposed a clean `getAvailablePorts(count)` API for the duplicate-port problem. Had a regex error in its grep that may have limited its code exploration.

| Metric | Claude Opus 4.6 | Codex GPT 5.4 | Gemini 3.1 Pro |
|---|---|---|---|
| Duration | 5:55 | 3:53 | 5:07 |
| Critical | 0 | 0 | 1 (downgraded) |
| Important | 1 | 2 | 2 |
| Suggestion | 2 | 1 | 0 |
| Design observations | 3 | 1 | 0 |
| False positives | 0 | 0 | 1 (severity) |
| Unique insights | `/api/steve-port` consumer, mock.ts coincidence | Restart ordering, flaky test | Silent startup failure |

Codex provided the highest signal-to-noise ratio with the fastest turnaround. Claude provided the deepest trace work and most design observations. Gemini's severity miscalibration on the proxy finding reduced its overall accuracy, but its identification of the silent startup failure was the most actionable finding in the review.

---

## Appendix: Original Reviews

### Claude Opus 4.6

## Executive Summary

This change replaces hardcoded port 9443 for Steve with dynamically allocated ports at startup, eliminating conflicts with other software using that port. The approach is clean: ports are allocated in the orchestrator, propagated to three consumers (Steve process, dashboard proxy, certificate handler), and the proxy middleware uses a wrapper pattern to support re-creation across restarts. The change is low risk with one important gap to address.

## Findings

### Critical Issues

None.

### Important Issues

1. **`/api/steve-port` endpoint has no visible consumer** — `dashboardServer/index.ts:91-92` (important, regression)

```ts
this.dashboardServer.get('/api/steve-port', (_req, res) => {
  res.json({ port: this.stevePort });
});
```

This new endpoint is registered but nothing in the codebase fetches it. If it's intended for consumption by the bundled rancher-dashboard (which is an external build artifact in `resources/rancher-dashboard/`), that's fine, but it should be documented as such. If no consumer exists yet, it's dead code.

Fix: Add a comment explaining who consumes this endpoint, or remove it until it's needed.

### Suggestions

1. **Websocket upgrade silently drops when proxies aren't initialized** — `dashboardServer/index.ts:139-141` (suggestion, regression)

```ts
if (key) {
  return this.proxies[key]?.upgrade(req, socket, head);
}
```

In the old code, `this.proxies` was populated at construction time via an IIFE, so `this.proxies[key].upgrade(...)` was always valid. The new code starts with `this.proxies = Object.create(null)` (line 30) and only populates it when `setStevePort()` is called. If a websocket upgrade arrives before that, the `?.` evaluates to `undefined`, the socket is never closed, and no log is emitted.

In practice this is unreachable: the dashboard button is disabled until K8s reaches STARTED state (as confirmed in `DashboardOpen.vue:17`), and `setStevePort()` is called before the UI is notified at `background.ts:1285-1293`. Still, a defensive cleanup would be cleaner.

Fix: Destroy the socket when the proxy is unavailable:
```ts
if (key) {
  if (this.proxies[key]) {
    return this.proxies[key].upgrade(req, socket, head);
  }
  console.log(`Proxy not ready for websocket upgrade to ${ req.url }`);
  socket.destroy();
  return;
}
```

2. **`desiredPort = 9443` remains in mock backend** — `backend/mock.ts:192` (suggestion, gap)

```ts
desiredPort = 9443;
```

This is a pre-existing value (from 2022, per `git blame`) and represents the Kubernetes API port, not Steve's port, so it's not a regression. However, 9443 is now coincidentally meaningful as the old Steve default. Worth confirming this value is intentional for the mock K8s backend.

## Design Observations

1. **Wrapper function pattern for dynamic proxy targets is well-chosen** (in-scope)

The approach of registering wrapper functions in express that dereference `this.proxies[key]` at call time (line 99) elegantly handles proxy recreation across Steve restarts without re-registering express routes. The comment at lines 95-97 explains the rationale clearly.

2. **Three-way port propagation** (future)

The Steve HTTPS port is propagated to three separate consumers: `Steve.start()`, `DashboardServer.setStevePort()`, and `setStevePort()` in networking — all from the state-changed handler in `background.ts:1281-1287`. If future changes add more consumers, this manual fan-out could become error-prone. A lightweight event (e.g., `mainEvents.emit('steve-port-changed', port)`) would let consumers self-register, but the current three-consumer count doesn't warrant the abstraction yet.

3. **Steve `start()` early-return doesn't update `httpsPort`** (future)

At `steve.ts:48-52`, if `start()` is called while Steve is already running, it returns early without updating `this.httpsPort` (set at line 54, after the guard). In the current flow this is safe: `stop()` is called on STOPPING, setting `isRunning = false` (line 100) before the next STARTED event fires. But if the lifecycle ever changes to allow hot-reconfiguring Steve's port without a stop/start cycle, this would silently use a stale port in `isPortReady()` (line 165).

## Testing Assessment

The new `networks.spec.ts` tests cover `getAvailablePort()` well:
- Port is greater than zero
- Port is actually bindable (exercises the TOCTOU window)
- Consecutive calls return distinct ports

Untested scenarios, ranked by risk:

1. **No integration test for the restart cycle** — the most important scenario (allocate new ports → recreate proxies → start Steve → verify proxy routes to new port) is not tested. This would require mocking Electron and the backend state machine, which may be impractical for a unit test.
2. **No test for `setStevePort` in networking module** — the certificate-error handler's `dashboardUrls` array uses the dynamic port, but this is deeply coupled to Electron's `app.on('certificate-error')` callback.
3. **No test for `/api/steve-port` endpoint** — if this endpoint has a consumer, its response format (`{ port: number }`) should be tested.

## Documentation Assessment

No documentation gaps. The TOCTOU risk in `getAvailablePort()` is documented in the code comment (lines 10-14 of `networks.ts`). The `TOCTOU` spelling addition to `expect.txt` is appropriate. Steve's `--help` confirms support for `--https-listen-port` and `--http-listen-port` flags.

---

### Codex GPT 5.4

**Verdict** Needs fixes before merge.

### Executive Summary

This change replaces Steve's fixed `9443` listener with dynamically chosen ports and teaches the dashboard proxy / certificate exception paths to follow the new HTTPS port. The main regression is in restart handling: the new port is published before Steve has actually moved, so a normal stop/start can leave the dashboard pointed at a dead port. There is also still an unresolved allocation race because the code picks "free" ports, releases them, and only binds them later.

### Findings

**Critical Issues**

None.

**Important Issues**

1. **Restart can publish a new Steve port while the old Steve process is still using the old one** — `background.ts:1281-1287` (important, regression)

```ts
const steveHttpsPort = await getAvailablePort();
const steveHttpPort = await getAvailablePort();

console.log(`Steve ports: HTTPS=${ steveHttpsPort } HTTP=${ steveHttpPort }`);
DashboardServer.getInstance().setStevePort(steveHttpsPort);
setStevePort(steveHttpsPort);
await Steve.getInstance().start(steveHttpsPort, steveHttpPort);
```

The new code updates the dashboard proxy target at `background.ts` line 1285 and the certificate exception list at `background.ts` line 1286 before `Steve.start()` at `background.ts` line 1287 has actually switched Steve over. On restart, `background.ts` line 1296 only sends `SIGINT`, and `pkg/rancher-desktop/backend/steve.ts` line 177 does not wait for process exit; if the old Steve is still alive, `pkg/rancher-desktop/backend/steve.ts` lines 48-51 return early without rebinding. That leaves `/api/steve-port` and the rebuilt proxies in `pkg/rancher-desktop/main/dashboardServer/index.ts` lines 74-76 and 91-92 advertising the new port even though Steve is still on the old one, and `background.ts` line 1293 then reports Kubernetes ready.

Fix: do not publish the new port until `Steve.start()` has confirmed it actually spawned on that port. At minimum, wait for `Steve.stop()` to observe process exit before choosing/publishing replacement ports, or make `Steve.start()` reject when it is still shutting down so the caller can retry instead of silently switching the dashboard to an unusable target.

2. **The allocator still has a bind race, so Steve can fail to start on the "dynamic" ports with no recovery** — `pkg/rancher-desktop/utils/networks.ts:16-24` (important, regression)

```ts
export function getAvailablePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address() as net.AddressInfo;
      server.close(() => resolve(addr.port));
    });
  });
}
```

`getAvailablePort()` at `pkg/rancher-desktop/utils/networks.ts` lines 16-24 explicitly releases the port before returning it, and `background.ts` lines 1281-1287 do this twice before `pkg/rancher-desktop/backend/steve.ts` lines 65-79 finally ask Steve to bind. That creates two new failure modes introduced by this change: another local process can claim either port in between, and the two sequential calls can legally hand back the same port. In both cases Steve will fail to bind, but `pkg/rancher-desktop/backend/steve.ts` lines 103-117 only log the error and `background.ts` line 1293 still advances the UI to ready.

Fix: reserve both ports until Steve owns them, or better, let Steve bind port `0` itself and report the chosen ports back to the Electron process. If the current design stays, at least detect equal ports, retry on bind failure, and propagate startup errors back to the caller instead of swallowing them.

**Suggestions**

1. **The new unit test asserts an OS behavior the implementation does not guarantee** — `pkg/rancher-desktop/utils/__tests__/networks.spec.ts:28-32` (suggestion, regression)

```ts
it('returns distinct ports on consecutive calls', async() => {
  const port1 = await getAvailablePort();
  const port2 = await getAvailablePort();

  expect(port1).not.toBe(port2);
});
```

This test is asserting a property that `getAvailablePort()` does not provide. Because `pkg/rancher-desktop/utils/networks.ts` lines 11-24 close the socket before returning, two consecutive calls may legitimately reuse the same ephemeral port on some kernels; that makes `pkg/rancher-desktop/utils/__tests__/networks.spec.ts` line 32 flaky and does not validate the real product requirement anyway.

Fix: drop this assertion and replace it with a test around the actual startup behavior, for example "Steve start retries/rejects when the selected HTTP and HTTPS ports collide" or a helper that reserves two distinct ports together.

### Design Observations

1. **Have Steve own port selection and report the bound ports back** `(in-scope)`
Passing `0` to Steve for its listeners and having it print or IPC-report the actual bound ports would remove the duplicated port state currently spread across `background.ts`, `dashboardServer`, and `main/networking`. That eliminates both the TOCTOU allocation race and the restart ordering bug, because the app would only publish ports Steve has already bound.

### Testing Assessment

1. The restart path is untested: no test covers `STOPPING` followed quickly by `STARTED`, where `background.ts` lines 1295-1296 and `steve.ts` lines 48-51 can leave the dashboard pointed at a new port before Steve has moved.
2. There is no test for bind failure after dynamic allocation, including the case where the chosen port is grabbed before Steve starts or the HTTP/HTTPS selections collide.
3. There is no integration coverage for the new dashboard contract (`/api/steve-port`) to verify the rd4 dashboard actually switches ports correctly across startup and restart.

### Documentation Assessment

The new `/api/steve-port` contract introduced in `pkg/rancher-desktop/main/dashboardServer/index.ts` should be documented somewhere in the dashboard integration notes or API contract docs; otherwise the dependency on Rancher Dashboard `2.11.1.rd4` is implicit rather than explained.

---

### Gemini 3.1 Pro

### Executive Summary

This change effectively implements dynamic port allocation for Steve and correctly routes the Dashboard Server to the new ports on each restart. However, because the dashboard proxy configuration is now initialized asynchronously, requests arriving during startup bypass the proxy and fall through to static fallback routes, returning HTML to API clients and leaving WebSockets hanging. Additionally, sequential dynamic port requests may yield the same port on some OSes, causing Steve to crash.

### Findings

#### Critical Issues

1. **Dashboard proxy gap during startup returns HTML for APIs and hangs WebSockets** — `pkg/rancher-desktop/main/dashboardServer/index.ts:94-96` and `139-141` (critical, regression)

```typescript
    ProxyKeys.forEach((key) => {
      this.dashboardServer.use(key, (req, res, next) => this.proxies[key]?.(req, res, next) ?? next());
    });
// ...
        if (key) {
          return this.proxies[key]?.upgrade(req, socket, head);
        }
```

Before this change, `this.proxies` was populated synchronously. If a client hit an API path (like `/v1/namespaces`) before Steve was ready, the proxy would attempt to connect to the static `9443` port, fail, and return a 598 error code with JSON.

Now, `this.proxies` is initialized as an empty object and remains empty until `setStevePort()` is called just before Steve starts. If a request arrives during this window:
- **HTTP APIs**: `this.proxies[key]` is undefined, so the `?? next()` fallback passes the request down the Express middleware chain. It hits the `*missing` catch-all route, returning HTTP 200 OK with the contents of `index.html`. API clients polling during startup will fail with JSON parsing errors.
- **WebSockets**: `this.proxies[key]?.upgrade` evaluates to `undefined`. The event handler silently finishes without calling `socket.destroy()`, leaving the client's socket hanging indefinitely.

**Fix**: Explicitly handle the "not ready" state in both places:
```typescript
    ProxyKeys.forEach((key) => {
      this.dashboardServer.use(key, (req, res, next) => {
        const proxy = this.proxies[key];
        if (proxy) {
          proxy(req, res, next);
        } else {
          res.status(503).json({ error: 'Steve API is not ready' });
        }
      });
    });

    // ... in the upgrade handler:
        if (key) {
          const proxy = this.proxies[key];
          if (proxy && proxy.upgrade) {
            return proxy.upgrade(req, socket, head);
          }
          socket.write('HTTP/1.1 503 Service Unavailable\r\n\r\n');
          socket.destroy();
          return;
        }
```

#### Important Issues

2. **Sequential port requests may return the same port** — `background.ts:1281-1282` (important, gap)

```typescript
          const steveHttpsPort = await getAvailablePort();
          const steveHttpPort = await getAvailablePort();
```

`getAvailablePort()` works by binding port `0` and immediately closing the server to free the port. Because the port was never connected to, it skips the `TIME_WAIT` state and becomes immediately available. Depending on the OS network stack behavior (particularly on Windows) and current ephemeral port exhaustion, the OS may hand out the exact same port on the subsequent `listen(0)` call. If `steveHttpsPort` and `steveHttpPort` are identical, Steve will fail to start because it cannot bind both listeners to the same port.

**Fix**: Change `getAvailablePort` to request multiple ports concurrently so they are both bound before either is released, guaranteeing uniqueness:
```typescript
export async function getAvailablePorts(count: number): Promise<number[]> {
  const servers = Array.from({ length: count }, () => net.createServer());
  const ports: number[] = [];

  for (const server of servers) {
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject);
      server.listen(0, '127.0.0.1', () => {
        ports.push((server.address() as net.AddressInfo).port);
        resolve();
      });
    });
  }

  for (const server of servers) {
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
  return ports;
}
```

3. **Unhandled Steve startup failure leaves the system in an inconsistent state** — `pkg/rancher-desktop/backend/steve.ts:120-124` (important, gap)

```typescript
    try {
      // ...
      await this.waitForReady();
    } catch (ex) {
      console.error(ex);
    }
```

If Steve fails to start (e.g., due to a TOCTOU port collision or identical HTTP/HTTPS ports as described above), `waitForReady()` throws an error. The `start()` method catches this error, logs it, and returns successfully. `background.ts` assumes Steve started correctly, the Dashboard Server creates proxies pointing to a dead port, and the UI state expects Steve to be available. Since port bindings are now dynamic and inherently race-prone, silent failures here are riskier.

**Fix**: Re-throw the error in `start()` or return a success boolean so `background.ts` can properly handle or retry the failure.

#### Suggestions

None.

### Design Observations

None.

### Testing Assessment

1. **Dashboard Proxy Startup State**: There are no tests verifying the behavior of the Dashboard Server when it receives API or WebSocket requests before `setStevePort()` is called. Testing this phase would have caught the `index.html` fallback bug.
2. **Identical Port Handling**: The test in `networks.spec.ts` assumes the OS will naturally return distinct ports. Mocking the OS to return identical ports (or forcing ephemeral port exhaustion) would test whether the application can safely handle or avoid port allocation collisions.

### Documentation Assessment

None.
