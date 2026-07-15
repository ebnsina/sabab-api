# Sabab — working notes for Claude

Application observability platform (errors, logs, traces, metrics). Go backend,
ClickHouse event plane, Postgres control plane, Redis Streams queue, MinIO/S3
artifacts, SvelteKit dashboard, own JS SDKs. Milestone plan lives in
`docs/plan.md` (gitignored — local reference only).

## UI / UX — treat this as a product, not a data dump

The dashboard is a tool people open when something is on fire. Every screen must
earn its space.

- **Never dump raw data to the UI.** Shape it. Decide what the user actually
  needs to see first, show that, and put the rest behind a click. A wall of
  fields is not a feature.
- **No clutter.** If a screen is hard to scan in two seconds, it is wrong.
  Prefer whitespace, hierarchy, and a clear primary action over density.
- **It must be understandable at a glance.** Severity by colour, counts
  formatted for humans, relative times ("3m ago") over ISO strings. If a
  reasonable engineer has to stop and decode the screen, redesign it.
- Think about the empty state, the loading state, the error state, and the
  "10,000 rows" state — every time, not just the happy path.
- Design dark-first (see `src/lib/styles/app.css`). Severity→colour lives in one
  CSS variable block; never hard-code a level colour in a component.

### Implementation — Tailwind v4 + a reusable component kit

- The dashboard uses **Tailwind v4** (CSS-first, `@theme` tokens — a build-time
  tool, zero runtime JS, so it fits the minimal-runtime-deps rule). Compose
  utilities; do not hand-write per-component CSS for what a token + utility
  already expresses, and avoid per-page `<style>` blocks.
- **Reusable components live in `src/lib/ui/`** (`Button`, `Input`, `Card`,
  `Badge`, `Spinner`, plus the `cx` class helper). Build a component once and use
  it everywhere — consistency by construction beats everyone remembering the same
  classes. Reach for a component before repeating utility strings.
- **Design tokens** are semantic CSS vars (`--surface`, `--text`, `--accent`,
  `--fatal`…) mapped into Tailwind via `@theme inline` in `app.css`, so
  `bg-surface`, `text-accent`, `ring-accent` all work and re-resolve on theme
  change.
- **Light + dark**, defaulting to the system setting, with a manual
  system/light/dark switch (`src/lib/theme.ts`, `ThemeToggle.svelte`). A tiny
  inline script in `app.html` stamps the saved theme before first paint to avoid
  a flash. Never hard-code a hex in a component — reference a token so both
  themes stay correct.

### Visual language — clean, modern, bold, professional

The look is deliberate: confident and modern, never funky or toy-like. Landing
page and dashboard share one visual language.

- **Shape.** One radius everywhere it counts: buttons, inputs, textareas,
  selects, cards and segmented controls all use **`--radius-xl` (12px)** — a
  button sits beside an input constantly, and a shared corner is what makes a
  toolbar read as one engineered unit rather than assembled parts. Segments
  inside a segmented control use a slightly tighter inner radius (~`0.6rem`) so
  they nest concentrically. Small chips/badges use `--radius-sm`. Fully-round
  (`rounded-full`) is reserved for genuinely circular things — avatars, status
  dots, progress tracks — never for buttons.
- **Focus.** One focus treatment everywhere: a **2px ring in the brand colour
  with a 2px offset** (the offset gap shows the surface behind, so the ring
  reads crisp on any background) plus a subtly lifted border. Use the
  `--focus-ring` box-shadow token; never leave a browser default outline.
- **Brand colour** (indigo accent — `#635bff` light, `#8b7cff` dark) is
  approved. Use it for the primary action, focus, and "needs attention", and
  sparingly elsewhere. It deliberately sits OFF the severity spectrum: amber
  stays reserved for `warning` severity, so the brand never reads as a warning.
  Never hard-code the hex — reference the `--accent` token so both themes stay
  correct.
- **Motion.** Apply Svelte transitions where they aid comprehension — content
  fading in on load, details sliding open, a toast easing in. Keep it
  **professional**: short durations (120–200ms), standard easing, no bounce or
  spring, nothing that draws attention to itself. Motion clarifies state change;
  it is never decoration. Respect `prefers-reduced-motion`.
- **Type & space.** Bold, clear hierarchy — a strong page title, quiet
  supporting text. Let whitespace do the work; density is earned, not default.
- **Depth over outlines.** Separate surfaces by ELEVATION — distinct shades of
  the base (near-black shades in dark, off-white → white in light) — not by
  lines. Both themes follow this. Use borders sparingly, and when you do, keep
  them a barely-there alpha (`--border` is a low-opacity black/white). A soft
  shadow lifts something genuinely floating (menu, modal, auth card); a heavy
  outline where a tone-step would do is wrong.
- **Icons must communicate.** Pick the glyph that says the thing: "ignore" is a
  muted bell (stop notifying), not an eye-off; "logs" is a console, not a
  hamburger; "trace" is a route, not a globe. HugeIcons has 40k+ free icons —
  search for the one that reads instantly, and centralise it by role in
  `src/lib/icons.ts`. A vague icon is worse than a text label.

## Dependencies — as few third-party libs as possible

- **Reach for the platform before a package.** Numbers, currency, dates,
  percentages, relative time, lists, pluralisation → the browser **`Intl`** API
  (`Intl.NumberFormat`, `Intl.DateTimeFormat`, `Intl.RelativeTimeFormat`,
  `Intl.ListFormat`). Do not add a formatting library for what `Intl` does.
- On the Go side, prefer the standard library. A dependency has to earn its
  place (pgx, clickhouse-go, go-redis, minio are load-bearing; a helper for
  three lines is not).
- Before adding any JS/TS dependency, ask whether a dozen lines of our own would
  do. If yes, write the dozen lines.
- Current sanctioned frontend deps: SvelteKit/Svelte, `@hugeicons/svelte` +
  `@hugeicons/core-free-icons` (icons), `@fontsource-variable/*` (self-hosted
  fonts). Adding to this list is a deliberate decision, not a reflex.

## Never expose internal details in public or user-facing surfaces

This is a hard rule. Anything a user or the public can see — the dashboard UI,
the public `/changelog`, marketing/landing copy, API responses, error messages,
commit messages on the public remote — must contain **zero** internal detail:

- No milestone or roadmap codenames (M0, M1, "M1.5", phase numbers).
- No internal architecture, table names, queue names, or tech-stack specifics
  that the user has no reason to see.
- No references to the internal plan (`docs/plan.md`) or future unreleased work.

Write user-facing text for the user: what changed for them, in their language.
Keep internal terminology to code comments, `CLAUDE.md`, and gitignored docs.
When in doubt, ask "would I put this on the product's website?" — if not, it does
not go in anything the user can see.

## Changelog — maintain from day one

- `CHANGELOG.md` at the repo root is the source of truth (Keep a Changelog
  format). Add an entry under `## [Unreleased]` for every user-facing change as
  it lands — do not batch it up at release time.
- The dashboard renders it publicly at `/changelog` (parsed by
  `src/lib/changelog.ts`, no markdown dependency). The page is reachable without
  a login (see `PUBLIC_PATHS` in `hooks.server.ts`).

## Verify before claiming done

Drive the real thing — the running stack, a real browser — not just tests. The
plan's milestones each have an explicit acceptance test; meet it and say so
plainly, or say what is still missing. Never imply something works that has not
been exercised.

## Git & commits

- Author every commit as **`ebnsina <ebnsina.me@gmail.com>`** (per-repo config
  already set). Do NOT add a `Co-Authored-By: Claude` trailer or any other
  identity.
- Remote uses the **`github-es`** SSH host alias
  (`git@github-es:ebnsina/sabab-api.git`).
- `docs/plan.md` and `data/` are gitignored — keep the public repo clean (no
  plans/roadmap, no secrets).
- Commit and push only when the user asks.

## Local environment

This machine runs a native Postgres (5432) and Redis (6379) that shadow Docker's
published ports, so the gitignored `.env` shifts the containers to 15432/16379
and moves the Go services to gateway :8090, api :8091 (processor health :8082,
alerter :8083). See `.env.example` for the full surface.

## Code conventions

- Comments explain **why**, not what — the constraint or trade-off a future
  reader can't see from the code. Match the surrounding density.
- ClickHouse schema decisions are checked against the installed
  `clickhouse-best-practices` skill and annotated with the rule they follow;
  the one place the errors table knowingly diverges (daily partitions) says why.
- Migrations are immutable once applied. Add a new file; never edit an old one.
