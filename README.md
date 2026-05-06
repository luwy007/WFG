# WFG

WFG is a macOS menu bar proxy controller built around the mihomo core. It provides a small SwiftUI control panel for subscription management, node selection, latency tests, rule mode switching, system proxy mode, and TUN mode.

The app is split into three main parts:

- `MacApp/WFG`: the macOS menu bar app and SwiftUI control panel.
- `engine`: a local Go REST service that owns app state, configuration, subscription parsing, and mihomo process control.
- `mihomo`: the actual proxy engine, downloaded into the app data directory at runtime and controlled through its external controller API.

## Build

Build the Go engine and macOS app with:

```bash
scripts/build-app.sh
```

The final app is copied to:

```text
MacApp/WFG.app
```

Xcode intermediate output is kept under:

```text
MacApp/.derived/
```

Useful environment variables:

- `CONFIGURATION=Debug|Release`: choose the Xcode configuration. Default: `Release`.
- `DERIVED_DATA_PATH=/path`: override Xcode derived data path.
- `FINAL_APP_DIR=/path`: override the final app output directory.
- `GOCACHE=/path`: override Go build cache.

## Runtime Data

WFG stores runtime files in:

```text
~/.wfg/
```

Important files:

- `app_config.json`: WFG app configuration, subscriptions, rules, selected node, proxy mode, and TUN preference.
- `mihomo_config.yaml`: generated mihomo configuration.
- `mihomo`: downloaded mihomo binary.
- `mihomo.log`: mihomo log output.
- `mihomo.pid`: PID file used by the normal-user `osascript` TUN fallback.
- `subscription_logs.json`: subscription refresh logs.
- `geo/`: GeoIP, GeoSite, and MMDB data files.

## High-Level Architecture

```text
macOS menu bar app
  |
  | starts/stops
  v
wfg-engine  (Go REST API on 127.0.0.1:19090)
  |
  | generates config, starts/stops, reloads, selects nodes, tests latency
  v
mihomo      (external-controller on 127.0.0.1:9090)
  |
  | handles proxy traffic
  v
network
```

The Swift app never talks to mihomo directly. It talks to `wfg-engine` through local HTTP APIs. The Go engine translates app-level actions into configuration changes, system proxy changes, mihomo REST calls, and process lifecycle operations.

## macOS Frontend

The macOS app lives in `MacApp/WFG`.

Key files:

- `WFGApp.swift`: app entry point. Ensures an engine is reachable when the app launches.
- `EngineLauncher.swift`: connects to an existing `wfg-engine` on `127.0.0.1:19090`, or launches a bundled development fallback if no service is running.
- `EngineAPI.swift`: typed HTTP wrapper used by the SwiftUI state layer.
- `AppState.swift`: main observable state store. Calls engine APIs and exposes app state to views.
- `StatusBarController.swift`: menu bar item and popover host.
- `Views/MainPopoverView.swift`: top-level popover layout and error banner.
- `Views/DashboardView.swift`: status, website connectivity tests, traffic summary, and mode switching.
- `Views/NodeListView.swift`: node list, node selection, and concurrent latency testing.
- `Views/SubscriptionView.swift`: subscription CRUD, refresh logs, auto-refresh settings.
- `Views/RulesView.swift`: routing rule editor.

Frontend call flow:

```text
SwiftUI View
  -> AppState method
  -> EngineAPI GET/POST/PUT/DELETE
  -> wfg-engine REST route
```

Examples:

- Start system proxy mode:
  `Dashboard/toolbar -> AppState.startInSystemProxyMode() -> /api/tun false -> /api/proxy/start`
- Start TUN mode:
  `Dashboard/toolbar -> AppState.startInTunMode() -> /api/tun true -> /api/proxy/start`
- Select a node:
  `NodeListView -> AppState.selectNode() -> /api/nodes/select -> mihomo /proxies/{group}`
- Test node latency:
  `NodeListView -> AppState.testLatency() -> /api/nodes/latency -> mihomo /proxies/{node}/delay`
- Refresh subscription:
  `SubscriptionView -> AppState.refreshSubscription() -> /api/subscriptions/{id}/refresh`

## Go Engine

The engine lives in `engine`.

Key files:

- `main.go`: parses flags, initializes the manager, handles parent-process shutdown, starts the REST API.
- `api/server.go`: Gin HTTP routes exposed to the macOS app.
- `core/manager.go`: central app manager for config, subscriptions, mihomo lifecycle, node selection, latency testing, and auto refresh.
- `core/config.go`: app config loading/saving and mihomo YAML generation.
- `core/subscription.go`: subscription download and parsing.
- `core/sublog.go`: subscription refresh log persistence.
- `core/sysproxy.go`: macOS system proxy enable/disable.
- `models/node.go`: app config, nodes, rules, proxy modes.
- `models/subscription.go`: subscription, user traffic info, refresh logs.

The engine listens on:

```text
127.0.0.1:19090
```

The mihomo external controller listens on:

```text
127.0.0.1:9090
```

Default proxy mixed port:

```text
127.0.0.1:7890
```

## Engine API Surface

Common routes:

- `GET /api/status`: app, proxy, selected node, and TUN status.
- `POST /api/tun`: enable or disable TUN mode. If mihomo is running, the engine restarts it in the background.
- `POST /api/proxy/start`: start mihomo and enable system proxy if not in TUN mode.
- `POST /api/proxy/stop`: stop mihomo and disable system proxy.
- `POST /api/proxy/mode`: switch `rule`, `global`, or `direct`.
- `GET /api/subscriptions`: list subscriptions.
- `POST /api/subscriptions`: add subscription and immediately refresh it.
- `PUT /api/subscriptions/:id`: update auto-refresh settings.
- `DELETE /api/subscriptions/:id`: remove subscription.
- `POST /api/subscriptions/:id/refresh`: manually refresh subscription.
- `GET /api/subscriptions/logs`: read subscription refresh logs.
- `DELETE /api/subscriptions/logs`: clear subscription refresh logs.
- `GET /api/nodes`: list nodes.
- `GET /api/nodes/latency?name=...`: test a node through mihomo delay API.
- `POST /api/nodes/select`: select active node in the `手动选择` group.
- `GET /api/rules`: list routing rules.
- `PUT /api/rules`: update routing rules.
- `GET /api/geo/status`: check Geo data.
- `POST /api/geo/update`: download Geo data and reload mihomo.
- `GET /api/ping`: test website connectivity.
- `GET /api/mihomo/*path`: simple mihomo API passthrough for debugging.

## mihomo Integration

WFG generates mihomo config from app config and subscription nodes in `core/config.go`.

Generated config includes:

- `mixed-port`: default `7890`.
- `external-controller`: default `127.0.0.1:9090`.
- `proxy-groups`:
  - `PROXY`: `url-test` group.
  - `手动选择`: `select` group used by WFG node switching.
- DNS config with fake-ip mode.
- routing rules from the WFG rules editor.
- optional TUN config when TUN mode is enabled.

When app config changes while mihomo is running, `Manager.UpdateConfig` regenerates `mihomo_config.yaml` and calls:

```text
PUT http://127.0.0.1:9090/configs?force=true
```

Node selection calls mihomo:

```text
PUT /proxies/手动选择
```

Node latency uses mihomo:

```text
GET /proxies/{node}/delay?timeout=5000&url=http://www.gstatic.com/generate_204
```

## System Proxy Mode vs TUN Mode

### System Proxy Mode

In system proxy mode:

```text
macOS apps -> system proxy 127.0.0.1:7890 -> mihomo -> network
```

The engine starts mihomo as a normal child process and enables the macOS system proxy through `core/sysproxy.go`.

### TUN Mode

In TUN mode:

```text
macOS routing table / utun -> mihomo TUN stack -> network
```

TUN mode requires elevated privileges to create and manage the virtual network interface. The preferred setup is to run `wfg-engine` as a root LaunchDaemon; then the engine can start and restart mihomo directly without `osascript` authentication prompts. If the engine is running as a normal user, WFG falls back to `osascript` for TUN startup.

When TUN is enabled, WFG disables the macOS system proxy to avoid proxy loops.

TUN config is generated with:

- `enable: true`
- `stack: mixed`
- `auto-route: true`
- `auto-detect-interface: true`
- `dns-hijack: any:53`

## Subscription Flow

Subscription lifecycle:

```text
SubscriptionView
  -> AppState.addSubscription / refreshSubscription / updateSubscription
  -> wfg-engine subscription API
  -> Manager.RefreshSubscriptionWithSource
  -> FetchSubscription
  -> parse nodes
  -> update app_config.json
  -> rebuild in-memory node list
  -> regenerate/reload mihomo config if running
  -> append subscription log
```

Manual refreshes are logged as `手动刷新`.

Auto refreshes are logged as:

- `自动刷新触发`
- `自动刷新成功`
- `自动刷新失败`

Auto refresh scheduling is handled by the Go engine once per minute. The interval is stored per subscription. When the user confirms a new interval, the engine records `auto_refresh_from`, so the next automatic refresh is counted from that confirmation time.

## Error Handling

The frontend uses `EngineAPI` to normalize local HTTP errors:

- engine unreachable
- request timeout
- request cancellation
- HTTP status errors
- decoding errors

Cancellation errors are intentionally ignored in the UI because SwiftUI often cancels tasks when a view disappears or a request is superseded.

Detailed errors can be opened from the error banner through the full error window in `MainPopoverView.swift`.

## Development Notes

Run engine tests:

```bash
GOCACHE=engine/.cache/go-build go -C engine test ./...
```

Build everything:

```bash
scripts/build-app.sh
```

Install the root engine service:

```bash
scripts/install-engine-service.sh
```

The service is installed as `com.wfg.engine`, listens on `127.0.0.1:19090`, and uses the current user's `~/.wfg` data directory. Uninstall it with:

```bash
scripts/uninstall-engine-service.sh
```

Run the final app:

```bash
open MacApp/WFG.app
```

Because WFG is a menu bar app, it sets `LSUIElement=1` and does not show a normal Dock icon while running.
