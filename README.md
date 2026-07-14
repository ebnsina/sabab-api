# Sabab

Application observability: **errors, logs, traces and metrics** in one place, so
you can see what broke, why, how slow it was, and what the app was doing at the
time.

The point is not four tools in one repo — it is that every signal carries the
same `trace_id`, `environment` and `release`. From an error you jump to the
trace; from the trace you read the logs it emitted; from a slow endpoint you see
the errors it threw. That join is the product.

> **Status: M0 — foundations.** The stack comes up, the schemas migrate, and the
> normalized event model is defined. Ingest, processing and the dashboard land
> in M1.

## Stack

| Layer | Choice | Why |
|---|---|---|
| Ingest + API | Go | The gateway must answer in single-digit milliseconds |
| Event store | ClickHouse | Immutable, enormous, only ever read in aggregate |
| Control plane | PostgreSQL | Issue state is mutable, transactional and small |
| Queue | Redis Streams | Behind an interface, so Kafka can replace it at scale |
| Object store | S3 / MinIO | Source maps and debug artifacts |
| Dashboard | SvelteKit | |

**Two databases on purpose.** Issue *state* — resolved? assigned to whom? muted
until when? — is mutable and low-volume, so it lives in Postgres. Event *bodies*
are immutable, enormous and only ever queried in aggregate, so they live in
ClickHouse. Conflating the two is the mistake that kills these systems.

**The queue is not optional.** Symbolication and grouping are slow and can fail.
The gateway acknowledges and enqueues; everything expensive happens behind the
queue. An observability tool must never become the reason its customers' apps
are slow.

## Quick start

Requires Go 1.26+, Docker and pnpm.

```bash
cp .env.example .env     # defaults work as-is on a clean machine
make dev                 # brings the stack up and migrates both databases
```

`make dev` is `make up` followed by `make migrate`. When it finishes you have
Postgres, ClickHouse, Redis and MinIO running, with both schemas applied.

```bash
make help                # every available target
make migrate-status      # what is applied, what is pending
make test                # go test ./... -race
make down                # stop, keeping data
make reset               # stop and delete every volume
```

### If a port is already in use

The compose stack publishes Postgres on 5432 and Redis on 6379. If you already
run either natively, **the native server silently wins** — your app connects to
the wrong database and reports something baffling like `role "sabab" does not
exist`. Move the published port in `.env` and update the matching DSN:

```bash
POSTGRES_PORT=15432
SABAB_POSTGRES_DSN=postgres://sabab:sabab@localhost:15432/sabab?sslmode=disable
```

`.env` is read by both docker compose and the Go services, so a port is
configured in exactly one place.

## Layout

```
cmd/
  migrate/            apply the Postgres + ClickHouse schemas
internal/
  config/             environment + .env loading
  logging/            slog: JSON in production, text in development
  health/             liveness (dependency-free) and readiness (checks deps)
  event/              the normalized model — the boundary of the system
  migrate/            migration runner: one loader, two drivers
  store/postgres/     control plane
  store/clickhouse/   event plane
migrations/
  postgres/           control plane schema
  clickhouse/         event plane schema
deploy/
  docker-compose.yml  the whole stack, one command
docs/
  wire-format.md      the Sabab Envelope — the SDK ↔ gateway contract
```

## Conventions that are not negotiable

- **Migrations are immutable.** Editing an applied migration is a hard error,
  not a warning — that is how a laptop and production quietly diverge. Add a new
  file instead.
- **ClickHouse migrations must be idempotent.** There is no transactional DDL
  there, so a file that fails halfway has to converge when re-run.
- **`project_id` is never read from a payload.** The gateway resolves it from
  the ingest key, so a client cannot write into someone else's project.
- **PII scrubbing runs before the first write**, in the processor. It cannot be
  bolted on later — by then we have persisted the data.
- **The SDK must never break the host app.** Every hook wrapped, all failures
  swallowed, bounded buffers, hard timeouts.

The ClickHouse schema is annotated with the official ClickHouse best-practice
rules it follows, and the one place it knowingly diverges says why.
