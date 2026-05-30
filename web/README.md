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
