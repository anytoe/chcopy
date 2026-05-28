# ClickHouse Copy

`chcopy` copies a curated slice of data from a source ClickHouse instance into a local one, driven by a YAML config. A soft guard prompts for confirmation when the target doesn't look local.

## Motivation

When you need real data on a local ClickHouse — a recent slice of production for debugging, or a stable fixture for tests — ad-hoc `INSERT INTO ... SELECT ... FROM remoteSecure(...)` scripts proliferate. `chcopy` replaces them with a single declarative YAML: named sets of tables and WHERE clauses, repeatable across machines, with a soft guard that prompts for confirmation when the configured target doesn't look local.

Common use cases:

- **Local development** — seed a local ClickHouse with a recent prod slice so feature work runs against realistic data.
- **CI fixtures** — pin a stable subset of prod data into a container for integration tests.
- **Schema-paired data** — combine with [`chsync`](https://github.com/anytoe/chsync) to bring both schema and a data slice to local in two commands.

## Requirements

- A reachable source ClickHouse (typically prod) accessible from your local instance via `remoteSecure()`.
- A running local ClickHouse instance.

## Installation

```sh
go install github.com/anytoe/chcopy@latest
```

Or build from source:

```sh
git clone https://github.com/anytoe/chcopy.git
cd chcopy
go build -o chcopy .
```

## Usage

```sh
chcopy --config examples/config.yaml --name dev_import
```

For a full end-to-end walkthrough with two Dockerized ClickHouse instances, see [`examples/README.md`](examples/README.md).

## Configuration

```yaml
import_configurations:
  - name: dev_import
    tables:
      - table: shop.users
        where: ""
        truncate: true
      - table: shop.orders
        where: "WHERE created_at >= '2026-05-01'"
        truncate: true
```

Fields:

- `name` — unique within the file.
- `table` — fully qualified `db.table`. Must already exist in the local instance.
- `where` — raw `WHERE ...` clause, or empty string for full table.
- `truncate` — if true, `TRUNCATE TABLE` runs locally before insert.

## CLI

| Flag | Description |
|---|---|
| `--config <path>` | YAML config file (required) |
| `--name <name>` | Named configuration to run (required unless `--list`) |
| `--list` | Print available config names and exit |
| `--dry-run` | Print SQL without executing |
| `--force` | Skip the non-local target confirmation prompt (required for non-TTY use) |

## Per-table behavior

Tables run sequentially in declared YAML order. For each table:

1. Print source row count for the slice (with the WHERE).
2. Print local row count before.
3. If `truncate: true`, `TRUNCATE TABLE` locally.
4. `INSERT INTO <table> SELECT * FROM remote(<source>, <table>, user, pw) <WHERE...>`.
5. Print local row count after.

All SQL executes on the local server; the source is reached via `remote()`, or `remoteSecure()` when `CHCOPY_SOURCE_PORT=9440` (ClickHouse's TLS native port). The same heuristic applies to the local connection — port `9440` ⇒ TLS, port `9000` ⇒ plain. Failures abort immediately — no partial-success bookkeeping.

## Safety guards

`chcopy` does not block on the write target — you are responsible for pointing it at the right server. As a soft guard, if `CHCOPY_LOCAL_HOST` does not resolve to localhost or a private / docker-bridge address (`10/8`, `172.16/12`, `192.168/16`), `chcopy` prompts for confirmation before proceeding. The prompt names the target host so you can verify it; only the literal answer `yes` continues — anything else (including empty input, `no`, EOF, Ctrl-C) aborts with a non-zero exit code.

For non-interactive use (CI, scripts), stdin is not a TTY and the prompt cannot be answered, so a non-local target aborts with a clear error unless you pass `--force` to confirm up front. `--dry-run` skips the check entirely since nothing is written.

Required env vars (`CHCOPY_LOCAL_*` and `CHCOPY_SOURCE_*`) must still be set, since without them there is nothing to connect to.

## Planned features

- Profiles — a top-level `profiles:` section that composes named import configurations. Example: profile `localdev` bundles `users` + `sales`; profile `sales` is just the `sales` config. Run with `chcopy --config ... --profile localdev`.
- Column projection & masking — per-table column allowlist/denylist, plus simple transforms (e.g. hash an `email` column) so PII never reaches local.
- Sampling — `limit: N` or `sample: 0.1` per table for fast smoke imports.
- Parallel table copies — opt-in worker count to copy independent tables concurrently.
- CLI table filters — `--only <table>` / `--skip <table>` to run a subset of a named config without editing YAML.
- Schema pre-flight — diff source vs local columns (name, type, order) before insert and abort with a clear message if they drift, pointing at [`chsync`](https://github.com/anytoe/chsync).

## Notes

Not battle-tested. Review your config and the printed SQL (use `--dry-run`) before running. No warranty of any kind.
