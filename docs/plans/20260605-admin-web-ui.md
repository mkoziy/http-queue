# Admin Web UI

Server-side HTML admin interface embedded in the existing Go server. Read-only inspect view for workers and job queues. Design matches the chaos report aesthetic exactly (same CSS variables, same panel/chip/mono typography).

## Context

- **Auth**: Basic Auth (same credentials as admin API). Browser shows native credential dialog via `WWW-Authenticate` header.
- **Hosting**: Embedded in the existing `http-queue` Go server — no separate process.
- **Rendering**: `html/template` + `embed.FS`. No JS frameworks, no external CDN.
- **CSS**: Identical to chaos report (`--bg: #f2f2f0`, `--panel: #f8f8f6`, monospace tables, `border-radius: 0`).
- **Actions**: Read-only inspect only. Write operations via API/curl.

## Pages

| Route | Content |
|---|---|
| `GET /admin/` | Workers table + list of all known queues (from DB scan) |
| `GET /admin/queues/{queue}?status=pending&cursor=...` | Jobs table with status tabs and cursor pagination |

Navigation: header `http-queue / admin` breadcrumb. No sidebar.

## Files to create / modify

### New: `queue/list.go` additions
- `ListQueues(db) ([]string, error)` — scan `queue:` prefix, collect unique queue names from `queue:{name}:{status}:{ulid}` keys.

### New: `api/templates/base.html`
Shared layout: `<html>`, `<head>` with all CSS, header nav with breadcrumb slot.

### New: `api/templates/home.html`
Extends base. Two sections:
1. Workers panel: table with `ID | registered_at | last_seen`. Cursor pagination links.
2. Queues panel: list of queue names as links → `/admin/queues/{name}`.

### New: `api/templates/queue.html`
Extends base. Two sections:
1. Status tabs: `pending (N) | reserved (N) | dead (N)` — links to `?status=X`. Active tab styled.
2. Jobs panel: table with `ID | attempts | created_at | expires_at | worker_id`. Cursor pagination links.

### New: `api/admin_ui.go`
`AdminUIHandler` struct with:
- `HandleHome(w, r)` — lists workers (limit=50, cursor from `?cursor=`) + all queues
- `HandleQueue(w, r)` — lists jobs for `{queue}` with `?status` and `?cursor`, counts all three statuses

Templates loaded via `//go:embed templates/*.html`.

### Modified: `api/router.go`
```go
mux.Handle("GET /admin/", adminAuth(http.HandlerFunc(uiHandler.HandleHome)))
mux.Handle("GET /admin/queues/{queue}", adminAuth(http.HandlerFunc(uiHandler.HandleQueue)))
```

### Modified: `api/middleware.go`
Add `WWW-Authenticate: Basic realm="http-queue admin"` header to `BasicAuth` 401 responses so browsers show the native credential dialog.

## CSS design tokens (from chaos report)

```css
--bg: #f2f2f0;
--panel: #f8f8f6;
--panel-2: #efefec;
--text: #171717;
--muted: #5d5d58;
--line: #d6d6d1;
--mono: "SFMono-Regular", ui-monospace, monospace;
--sans: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
```

Status indicators (inline in table cells, not separate page elements):
- `pending` — muted gray text
- `reserved` — `#b8860b` (dark amber)
- `dead` — `#8b0000` (dark red)

## Implementation steps

- [x] Add `ListQueues()` to `queue/list.go`
- [x] Create `api/templates/base.html` with full CSS matching chaos report
- [x] Create `api/templates/home.html` (workers + queues)
- [x] Create `api/templates/queue.html` (jobs + status tabs + pagination)
- [x] Create `api/admin_ui.go` with `AdminUIHandler`, template embed, both handlers
- [x] Register `/admin/` and `/admin/queues/{queue}` routes in `router.go`
- [x] Add `WWW-Authenticate` header to `BasicAuth` 401 in `middleware.go`
- [x] Manual smoke test: open browser at `localhost:8080/admin/`
