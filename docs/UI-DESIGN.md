# Observatory UI Design — M1 Dev Console (U0)

> Status: M1 design baseline, implemented in U0–U4 and U6a. Scope is the dev console only.
> Later milestones add Overview, Metrics, Durability Lab, Replication Lab, and
> Interview Mode; their placeholders are named here but not designed yet.
>
> Sources of truth: `docs/PLAN.md` §3.2/§6, ADR-013…ADR-019, `web/tokens/`.

## 1. Purpose

The Observatory is an internal dev console for Antares, reached at
`http://127.0.0.1:6390`. It must make the server's behavior visible while the
remaining server phases are built: type a command and see the exact RESP wire
traffic, browse the keyspace through SCAN, and keep an eye on health. It is not
a general-purpose Redis GUI (ADR-019).

M1 success = we reach for it daily during P6–P10, not a marketing page.

## 2. Design principles

1. **One accent, one job.** Antares red-orange marks the product and the primary
   action. It never encodes status.
2. **Status never relies on color alone.** Every ok/warn/crit/info state carries
   an icon or text label; color is reinforcement.
3. **Dark by default.** A localhost dev tool is used next to a terminal. No light
   theme in M1.
4. **Density with air.** Monospace for identifiers, payloads, and counters;
   system sans for prose and labels. 4px spacing grid.
5. **Calm rendering.** Live data may arrive at 10 Hz, but the UI paints at most
   4 Hz and never animates layout. Jank is a bug.
6. **Honest states.** Loading, empty, disconnected, and error are designed, not
   afterthoughts. Unverified claims render as "not verified" (ADR-018).
7. **Keyboard first.** Tab order follows the visual order; `/` focuses the
   command input; `Esc` closes drawers; visible focus rings everywhere.
8. **AA contrast.** Body text ≥ 4.5:1, large text and UI glyphs ≥ 3:1.

## 3. Design tokens

Tokens live in `web/tokens/tailwind.config.js` (the Tailwind sketch) and are
mirrored as CSS variables in `web/src/index.css` during U3.

### 3.1 Color

| Token | Value | Use | Contrast on `bg` |
|---|---|---|---|
| `bg` | `#0b0d10` | app background | — |
| `surface` | `#14171c` | panels, cards | — |
| `surface-2` | `#1c2027` | inputs, hover rows | — |
| `border` | `#2a303a` | dividers, input borders | 1.5:1 (decorative) |
| `text` | `#e6e9ef` | primary text | 15.8:1 |
| `text-muted` | `#9aa3b2` | labels, secondary | 7.4:1 |
| `text-faint` | `#6b7484` | timestamps, hints | 4.6:1 |
| `accent` | `#ff5a3c` | brand, primary action, focus | 6.1:1 |
| `accent-soft` | `#3a1712` | accent-tinted surfaces | — |
| `ok` | `#3ddc97` | success state + icon/label | 9.7:1 |
| `warn` | `#f5c451` | degraded state + icon/label | 11.6:1 |
| `crit` | `#ff6b81` | failure state + icon/label | 6.4:1 |
| `info` | `#5cc8ff` | neutral information + icon/label | 9.2:1 |

Semantic colors are also given distinct glyphs: `✓ ok`, `! warn`, `× crit`,
`i info`, so color-blind users never lose the signal.

### 3.2 Type

| Token | Value |
|---|---|
| `font-sans` | `ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif` |
| `font-mono` | `ui-monospace, "SFMono-Regular", "JetBrains Mono", Menlo, monospace` |
| `text-xs` | 11px / 16px |
| `text-sm` | 13px / 20px |
| `text-base` | 14px / 22px |
| `text-lg` | 16px / 24px |
| `text-xl` | 20px / 28px |

Body size is 14px because this is a dense console, not a reading surface.
Command input and payloads are 13px mono.

### 3.3 Spacing, radius, elevation, motion

- Spacing: `1=4px`, `2=8px`, `3=12px`, `4=16px`, `5=20px`, `6=24px`, `8=32px`.
- Radius: `sm=4px` (chips), `md=6px` (inputs/buttons), `lg=10px` (panels/drawer).
- Elevation: borders carry structure; one shadow (`0 12px 32px rgba(0,0,0,.45)`)
  for the drawer and command palette only.
- Motion: 120ms ease-out for hover/focus, 160ms for drawer; none for data cells.
  `prefers-reduced-motion` removes non-essential transitions.

### 3.4 Tailwind sketch

`web/tokens/tailwind.config.js` exports the palette and font families as
`theme.extend`. U3 imports that object into `web/tailwind.config.js`; components
use semantic names (`bg-surface`, `text-muted`, `border-border`, `text-ok`)
rather than raw hex values.

## 4. Shell

Persistent frame at every route:

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│  ● Antares Observatory        antares 127.0.0.1:6380  ✓ up 45s  ⚠ redis off │  status bar 44px
├───────────────┬──────────────────────────────────────────────────────────────┤
│  Playground   │                                                              │
│  Keyspace     │   route content                                              │
│               │                                                              │
│  ─────────    │                                                              │
│  Overview  M2 │                                                              │
│  Metrics   M2 │                                                              │
│  Labs      M3 │                                                              │
│               │                                                              │
│  v0.1.0 dev   │                                                              │
└───────────────┴──────────────────────────────────────────────────────────────┘
   nav 220px        content, max-width 1440px, padding 24px
```

- Left nav is 220px on ≥ 900px; collapses to a 56px icon rail below 900px.
  Below 640px it becomes a top row with horizontal scroll; M1 is laptop-first
  (ADR-019).
- The status bar is always visible. It shows: product name, target address,
  Antares health, optional reference-Redis health, uptime, and version.
- Disabled future routes are shown in `text-faint` with a milestone tag; they
  are not links.

## 5. Playground (U4)

```text
┌ Playground ──────────────────────────────────────────────────────────────────┐
│ target: [antares ▾]   presets: PING  SET/GET  TTL  LIST  HASH  SET  SCAN     │
│ ┌──────────────────────────────────────────────────────────────────────────┐ │
│ │ GET user:1                                                           ⏎   │ │
│ └──────────────────────────────────────────────────────────────────────────┘ │
│ history:  GET user:1 · SCAN 0 COUNT 50 · HSET h f v                          │
├───────────────────────────────┬──────────────────────────────────────────────┤
│ RESPONSE                      │  ✓ bulk 12 B · 0.42 ms                       │
│ ┌───────────────────────────┐ │  ┌─────────────────────────────────────────┐ │
│ │ "Alice"                   │ │  │ [ ] Wire trace                         │ │
│ └───────────────────────────┘ │  │ > *2\r\n$3\r\nGET\r\n$6\r\nuser:1\r\n   │ │
│                               │  │ < $5\r\nAlice\r\n                       │ │
│                               │  └─────────────────────────────────────────┘ │
└───────────────────────────────┴──────────────────────────────────────────────┘
```

- Input accepts an inline command (same quoting as redis-cli is **not**
  required in M1; whitespace-split is enough, with double quotes supported).
- Enter executes. Up/Down walk history. History is capped at 100 and persisted
  in `localStorage`.
- Presets are one click; they fill the input, they do not auto-run.
- The response renderer has one view per RESP2 type: simple, error, integer,
  bulk (string, quoted, with byte count), array (collapsible tree), and null.
  A binary bulk is labelled `binary`, with escaped preview and byte size.
- The wire panel shows the canonical request and reply RESP bytes with CRLF
  escaped (`\r\n`). It is off by default and toggled per session.
- Latency is measured by the bridge around the round trip and shown with two
  decimals of a millisecond. No client-side timing is claimed.
- On transport failure the response panel becomes a `crit` state with the
  bridge's error text and the last successful latency remains visible.
- While a command is in flight the input keeps its value and shows a subtle
  progress indicator; a second Enter is ignored until the reply arrives.

### 5.1 Flow

```text
type command ──▶ split/validate ──▶ POST /api/exec {args,target}
     │                                      │
     │                                      ├─ ok ──▶ render reply + trace + latency
     │                                      └─ err ─▶ crit panel (transport/bridge)
     └─ empty ──▶ no-op (input keeps focus)
```

## 6. Keyspace (U6a)

```text
┌ Keyspace ────────────────────────────────────────────────────────────────────┐
│ filter [*____________]  type [any ▾]  count [100 ▾]        ↻ rescan  cursor 0 │
├──────────────────────────────────────────────────────────────────────────────┤
│ KEY                            TYPE      TTL          PREVIEW                │
│ user:1                         string    59s          5 B                    │
│ queue:jobs                     list      persistent   3 items                │
│ session:42                     hash      persistent   2 fields               │
│ tags                           set       12s          3 members              │
│ …                                                                             │
├──────────────────────────────────────────────────────────────────────────────┤
│ showing 100 · cursor 184 · [load more]                                       │
└──────────────────────────────────────────────────────────────────────────────┘
                                             ┌ detail drawer ──────────────────┐
                                             │ user:1                    [×]   │
                                             │ type string · ttl 59s           │
                                             │ length 5 bytes                  │
                                             │ value "Alice"                   │
                                             │ read-only in M1 · U6b mutates   │
                                             └─────────────────────────────────┘
```

- The table is fed by `GET /api/keys?cursor=&count=&match=&type=`, which wraps
  `SCAN` and annotates each key with `TYPE`, `PTTL`, and a cheap size summary.
  Cursor is opaque text.
- Default page is 100 keys; options 50/100/500. "Load more" appends the next
  page. The cursor is displayed so SCAN's opaque semantics are visible.
- Rows are virtualized (TanStack Virtual) so a 10k-key browse does not mount
  10k DOM rows. The page itself is bounded by `count`.
- Type badges use neutral text labels (`string`, `list`, `hash`, `set`), not
  color alone.
- TTL column: `-1` renders `persistent`; `-2` renders `gone` (key expired between
  SCAN and lookup); otherwise `12s` / `1m 04s`.
- The preview column is a size summary, not a value read: string `STRLEN` as
  `N B`, list `LLEN` as `N items`, hash `HLEN` as `N fields`, set `SCARD` as
  `N members`. This keeps the per-row cost to one extra command and never pulls
  a large value into the bridge. Missing previews render `—`.
- Clicking a row opens the detail drawer, which fetches `TYPE`, `PTTL`, `STRLEN`
  or `LRANGE/LLEN`, `HGETALL/HLEN`, `SMEMBERS/SCARD` through `/api/exec`.
  Large string values (`>64 KiB`) and collections (`>200` entries) are summarised
  rather than fetched whole. Mutating controls are out of scope for M1; the
  drawer is read-only.
- Empty keyspace: centered `No keys match` with a hint to run
  `deploy/demo/seed.sh`. Error: `crit` banner with retry.

### 6.1 Flow

```text
open route ──▶ GET /api/keys (cursor=0) ──▶ table
      filter/type change ──▶ reset cursor ──▶ GET /api/keys
      load more ──────────▶ GET /api/keys?cursor=<next> ──▶ append
      row click ──────────▶ drawer ──▶ /api/exec (TYPE, PTTL, value)
      key expired (TTL -2) ─▶ row marked `gone`, next rescan drops it
```

## 7. Status bar and health

`GET /api/health` probes both targets with `PING` under a 500ms timeout.

| State | Label | Glyph | Color |
|---|---|---|---|
| target up | `up <latency>` | `✓` | ok |
| target down | `down` | `×` | crit |
| reference not configured | `redis off` | `!` | warn |
| bridge up, target down | `bridge up, server down` | `!` | warn |

The bar refreshes every 4 seconds while the tab is visible; hidden tabs keep the
last value and stop polling (`visibilitychange`). SSE updates use the same
state machine; HTTP polling is the fallback.

## 8. Render policy (10 Hz in, 4 Hz out)

- The bridge polls `INFO` at 4 Hz and pushes SSE `info` events. The SPA stores
  samples in a ref/queue and flushes to React state at most every 250ms.
- Charts (M2) ingest up to 10 Hz but paint on `requestAnimationFrame` at ≤ 4 Hz;
  the ring buffer keeps the last 5 minutes at 1 s resolution.
- Data cells update by text replacement only; no layout animation.
- Pause/freeze controls arrive in U7. M1 has no live charts.
- SSE reconnect is browser-native (`EventSource`, 2s retry). The SPA shows
  `reconnecting` after 3s without an event and falls back to 4s polling.

## 9. States and accessibility

- **Loading:** skeleton rows/bars with `aria-busy="true"`, never a full-screen
  spinner.
- **Empty:** one sentence + one action.
- **Error:** inline panel with the raw error text, a retry button, and the
  affected endpoint. Errors are `role="alert"`.
- **Focus:** 2px accent ring with 2px offset; focus is never removed without a
  replacement.
- **Live regions:** connection state changes announce politely; command errors
  are not announced twice.
- **Contrast:** every text/semantic token above meets AA on `bg`/`surface`.
- **Reduced motion:** honors `prefers-reduced-motion: reduce`.
- **Keyboard:** `Esc` closes the drawer, arrow keys walk command history, Enter
  submits. `/` command focus and `g p` / `g k` route jumps are deferred to U12
  polish.

## 10. API contract used by M1

All responses are JSON unless noted. Errors use HTTP status + `{"error":"..."}`.

```http
GET  /api/health
     -> {"status":"ok","antares_addr":"127.0.0.1:6380",
         "antares":{"ok":true,"latency_ms":0.31},
         "redis":{"ok":false,"error":"not configured"},"version":"dev"}

POST /api/exec
     {"args":["GET","user:1"],"target":"antares"}
     -> {"target":"antares",
         "reply":{"type":"bulk","value":"Alice"},
         "request":"*2\\r\\n$3\\r\\nGET\\r\\n$6\\r\\nuser:1\\r\\n",
         "raw":"$5\\r\\nAlice\\r\\n",
         "latency_ms":0.42}

GET  /api/keys?cursor=0&count=100&match=*&type=
     -> {"cursor":"184","keys":[
          {"key":"user:1","type":"string","ttl_ms":59000,"preview":"5 B"},
          {"key":"tags","type":"set","ttl_ms":-1,"preview":"3 members"}]}

GET  /api/info?section=server
     -> {"raw":"# Server\\r\\nredis_version:7.4.11\\r\\n...",
         "fields":{"redis_version":"7.4.11","uptime_in_seconds":"45"}}

GET  /api/events/stream          (text/event-stream)
     event: info
     data: {"fields":{...},"at_ms":1690000000000}

GET  /                            SPA (fallback to index.html)
```

`target` defaults to `antares`; `redis` is available only when the bridge was
started with `-redis`. A reply of type `error` is still HTTP 200 — it is a
successful Redis exchange. HTTP 4xx/5xx means the bridge or transport failed.

## 11. M1 file map

| Area | Files |
|---|---|
| Design | `docs/UI-DESIGN.md`, `web/tokens/tailwind.config.js` |
| Client | `internal/client/client.go`, `reply.go`, tests |
| Bridge | `cmd/observatory/{main,bridge,bridge_test}.go` |
| SPA | `web/{package.json,vite.config.ts,tsconfig*.json,index.html,tailwind.config.js}`, `web/src/**` |
| Demo | `deploy/demo/seed.sh`, `Makefile` targets (`showcase-dev`, `ui-build`) |
