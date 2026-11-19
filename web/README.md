# mem Dashboard Frontend

A responsive, high-performance web dashboard for `mem` (the local long-term memory store for AI agents), built with React 19, TypeScript strict mode, Mantine 9, TanStack Query 5, CodeMirror 6, and MSW 3.

## Tech Stack & Architecture

- **Framework**: React 19 + TypeScript (strict mode enabled with zero `any`)
- **Build & Dev**: Vite 6
- **UI Components & Styling**: Mantine 9 (`@mantine/core`, `@mantine/hooks`, `@mantine/notifications`) + Tabler Icons
- **Routing**: React Router 7 (`react-router-dom`)
- **Data Fetching & Caching**: TanStack Query 5 (`@tanstack/react-query`)
- **Editor**: CodeMirror 6 (`@uiw/react-codemirror`, `@codemirror/lang-markdown`)
- **Markdown Rendering**: `react-markdown` + `remark-gfm`
- **Mocking**: Mock Service Worker 3 (`msw`) with an in-memory mock database populated with 4 contexts and 70+ realistic memories
- **Testing**: Vitest + React Testing Library + `@testing-library/jest-dom` + MSW node server
- **Linting & Formatting**: ESLint (flat config) + Prettier

## Architectural Principles

1. **Feature-Based Modular Structure**:
   - `src/features/contexts/`: Contexts list, auto-generated temp contexts, expiration badges & updating, safe slug-confirmed deletion.
   - `src/features/memories/`: Memory list (server filter, sort, pagination, collapsible folder tree / flat toggle), full-featured memory editor (CodeMirror 6, Markdown preview, tags, metadata bar, size check, safe save with `if_version`, append modal, delete confirmation, conflict resolution dialog).
   - `src/features/default-file/`: Pinned editor for `global/readme.md` with deletion protection.
   - `src/features/search/`: Spotlight modal (Ctrl+K / Cmd+K), full-page search results with scoped context filtering and degraded vector model notices.
   - `src/features/status/`: Server metrics, storage and vector index synchronization, llama-server sidecar model statuses.
2. **Strict REST & API Client**:
   - `src/api/client.ts`: Typed fetch client wrapping standard HTTP methods with custom `ApiError` class (holding `code`, `status`, `details`, `response`).
   - Supports Bearer token authentication via URL query param `?token=...` (persisted to `sessionStorage` and stripped from URL for cleanliness), custom `Authorization: Bearer <token>` headers, and 401 callback interception.
3. **Mobile-First Responsiveness**:
   - **Viewports tested**: 360x640 (small mobile), 390x844 (mobile), 768x1024 (tablet), 1440x900 (desktop).
   - **Navigation**: Collapses into a full-height slide-out Drawer toggled by a header burger button under the `sm` breakpoint (768px).
   - **List & Editor**: Side-by-side on desktop/tablet; transitions to separate full-screen views on mobile (`list -> editor` with a prominent "Back to list" button).
   - **Editor Modes**: Split side-by-side editor/preview on desktop; mobile transitions to tabs (`Edit | Preview`).
   - **Modals**: Transition to full-screen modals on small screens.
   - **Touch Targets**: Minimum 44px tap targets for mobile usability.
   - **Form Inputs**: Font sizes set to `>= 16px` to prevent iOS Safari auto-zoom on focus.
   - **Safe Areas**: `viewport-fit=cover` and CSS env variables (`safe-area-inset-*`).
   - **Zero Horizontal Scroll**: Verified `scrollWidth === clientWidth` at 360px.
4. **Self-Contained Offline Build**:
   - Zero CDN requests or external web fonts in the production bundle.
   - All icons, styles, fonts, and assets are bundled locally.

## Scripts

```bash
# Install dependencies
npm install

# Run development server with real backend proxy (or mock if VITE_API_MODE=mock)
npm run dev

# Run development server explicitly with mock data
npm run dev:mock

# Run TypeScript strict typecheck
npm run typecheck

# Run ESLint
npm run lint

# Run automated tests (Vitest + React Testing Library)
npm test

# Run tests in watch mode
npm run test:watch

# Build production bundle (for real backend, output to web/dist)
npm run build

# Build production bundle with MSW mock service worker for standalone demos
npm run build:mock

# Preview production mock build (configured for ngrok tunnels on port 4173)
npm run preview:mock
```

## Tunneling with ngrok

To share the mock dashboard preview with a mobile phone or remote browser:
```bash
# 1. Build and preview mock app locally
npm run build:mock
npm run preview:mock

# 2. In another terminal, tunnel port 4173
ngrok http 4173
```
The Vite preview configuration has `allowedHosts` set to accept `.ngrok-free.app`, `.ngrok-free.dev`, `.ngrok.app`, and `.ngrok.io`.

## Directory Structure

```
web/
├── docs/
│   └── screenshots/       # Browser screenshots at 1440x900, 768x1024, 390x844, 360x640
├── public/
│   ├── favicon.svg        # Local SVG favicon
│   └── mockServiceWorker.js # MSW service worker
├── src/
│   ├── api/               # Typed REST API client & endpoint functions
│   ├── app/               # Application shell, router, providers, error boundary
│   ├── features/          # Feature-based domain modules
│   │   ├── contexts/      # Context navigation, creation, expiry, deletion
│   │   ├── default-file/  # Special-cased global/readme.md editor
│   │   ├── memories/      # Memory list, folder tree, editor, append, conflict dialog
│   │   ├── search/        # Ctrl+K spotlight & full-page search results
│   │   └── status/        # System status, sidecars, vector index sync
│   ├── lib/               # Shared utilities, validation, formatters
│   ├── mocks/             # In-memory mock database & MSW request handlers
│   └── test/              # Vitest test setup, polyfills, and render utilities
├── CONTRACT-CHANGES.md    # Proposals and observations on .prompts/_api.md
├── PROGRESS.md            # Milestones and PREVIEW-READY flag
├── README.md              # Project documentation
├── REPORT.md              # Comprehensive engineering report
├── package.json           # Pinned dependencies and scripts
└── vite.config.ts         # Vite configuration with proxy and preview hosts
```
