# web/scripts

## screenshots.mjs

Playwright-based screenshotter that captures the gitstate UI into the PNGs the
README and docs reference.

### Usage

```bash
# Seed a deterministic demo database, build the SPA, and start the daemon:
cargo run -p gitstate-cli -- seed --demo
(cd web && npm run build)
cargo run -p gitstate-cli -- serve --port 8080

# Then, from web/:
npm run shots
```

There is no auth — the daemon serves the SPA and the JSON API on one origin, so
every route is reachable directly.

The daemon serves `web/dist`, so **build before capturing** or the shots will
show the previous build.

### Environment variables

| Variable   | Default                  | Description                          |
| ---------- | ------------------------ | ------------------------------------ |
| `BASE_URL` | `http://localhost:8080`  | the daemon (serves both SPA and API) |
| `OUT`      | `../../docs/screenshots` | output directory                     |

### Captured pages

Dark theme (the product's default look): `/dashboard`, `/insights` (full page),
`/repos`, the first repo's detail page, `/contexts`, `/categories`, `/classify`,
`/taxonomy`, `/settings`.

Light theme: `/dashboard` and `/insights`, the two screens carrying the most
chrome.

Pages fail independently — one broken route won't stop the rest — but the script
exits non-zero if any failed.

### Why it waits on `svg[data-chart]`

Charts render only after `/api/analytics` resolves. Waiting on the heading alone
captures a spinner where the chart should be. The selector is `data-chart` and
not `role="img"` because icon `<svg>`s carry that role too — the same reason the
e2e suite uses these hooks (see `web/tests/README.md`).

### Determinism

`gitstate seed --demo` derives every id, hash and timestamp from a fixed anchor
via SHA-256, so re-seeding reproduces byte-identical rows and repeated capture
sessions stay pixel-stable.
