# seasonfill-web

React + Vite SPA bundled as the Seasonfill web UI. Built with React 19, TanStack Query, react-hook-form, shadcn/ui, Tailwind CSS, and i18next (EN/RU).

## Requirements

- Node.js >= 22.x (the test suite uses ESM-only imports that fail on Node 20 with `ERR_REQUIRE_ESM`)
- npm

## Dev

```bash
npm install
npm run dev
```

Vite serves the app on http://localhost:5173 by default. API calls proxy to the Seasonfill backend per `vite.config.ts`.

## Build

```bash
npm run build
```

Emits the production bundle to `dist/`.

## Test

```bash
npm run test
```

Runs Vitest once. Use `npm run test:watch` during development.

## Lint

```bash
npm run lint
```

Runs ESLint over the source tree. CI requires zero warnings.

## Design tokens

The design system source of truth lives in `src/index.css` inside the Tailwind
v4 `@theme` block. All colors are OKLCH; status hues come in `<token>` /
`<token>-dim` pairs (e.g. `--color-ok` + `--color-ok-dim`). Cool-grey surfaces
use hue 270. The accent (default teal hue `175`) is exposed as
`--accent-h` in `:root` — flipping it in-runtime re-tints the entire UI:

```js
document.documentElement.style.setProperty('--accent-h', 255) // → blue
document.documentElement.style.setProperty('--accent-h',  75) // → amber
```

### Extending a Badge variant

In `src/components/ui/badge.tsx`, add the new variant inside
`badgeVariants.variants.variant`, pairing the status text-color with its dim
background and a translucent border:

```ts
foo: "text-foo border-foo/35 bg-foo-dim",
```

The `foo` slug must correspond to a `--color-foo` + `--color-foo-dim` pair in
`@theme`. Then use `<Badge variant="foo">…</Badge>` anywhere.

### App shell

The two-pane app shell lives in `src/components/shell/`. `AppShell` composes
`AppSidebar` (244px) + `AppTopBar` + a scrollable content region. The topbar
reads the page title from `PageTitleProvider` (mounted in `ProtectedLayout`);
individual pages call `useSetPageTitle("…")` once on mount to set it.
Page-level action buttons (e.g. "Refresh" + "Run scan") mount via
`usePageActions().setActions(...)`.

## Routes

| Route | Component | Purpose |
|---|---|---|
| `/` | `Dashboard` | Homepage: hero greeting + imported-series grid + right-rail placeholders |
| `/scans` | `Scans` | Scan list & detail pages |
| `/grabs` | `Grabs` | Grab queue & detail pages |
| `/decisions` | `Decisions` | Manual decision log |
| `/instances` | `Instances` | Sonarr instance management |
| `/settings` | `Settings` | User settings & config |

Dashboard is the entry point for operators; it renders empty / first-run /
error states gracefully. All routes require authentication via `ProtectedLayout`.
