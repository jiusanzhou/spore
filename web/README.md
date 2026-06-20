# web/

This directory holds **only** the `//go:embed` machinery and the built dist/.

The actual frontend source lives under [`../web-v2/`](../web-v2/) — a Vite +
React 19 + TypeScript + Tailwind v4 SPA.

## How it works

- `web-v2/` is the source-of-truth source tree. Edit there.
- `make web-build` runs `pnpm build` in `web-v2/` and copies `web-v2/dist`
  into `web/dist`.
- `web/embed.go` does `//go:embed dist/*` so the built bundle ships inside
  the `spore`, `spore-acp-server`, and `spore-mcp-server` binaries.
- The Go API server (`internal/api/server.go`) mounts the embedded FS at
  `/` for a single-page-app experience.

## Why the indirection?

The Go embed directive can't reach outside its own package directory, so
the build output has to land in `web/dist/` even though sources live in
`web-v2/`. This split lets us:

1. Keep the JS toolchain (Node, pnpm, Vite) cleanly contained.
2. Avoid polluting the Go module with `node_modules/` or build artifacts.
3. Roll forward the frontend stack without touching `web/embed.go`.

## Dev loop

```sh
# Terminal 1 — Go API server
go run ./cmd/spore run --api-port 9292 ...

# Terminal 2 — Vite dev server (proxies /api → :9292)
cd web-v2 && pnpm dev
```

Open http://localhost:5173 (Vite default). For a release-shaped run,
`make build` produces a single `bin/spore` with everything baked in.
