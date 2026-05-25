# ClickHouse Copy

`chcopy` copies a curated slice of data from a source ClickHouse instance into a local one, driven by a YAML config. Local is the only allowed write target.

## Motivation

When you need real data on a local ClickHouse — a recent slice of production for debugging, or a stable fixture for tests — ad-hoc `INSERT INTO ... SELECT ... FROM remoteSecure(...)` scripts proliferate. `chcopy` replaces them with a single declarative YAML: named sets of tables and WHERE clauses, repeatable across machines, with hard guards that prevent writes to anything other than localhost.

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

## Per-table behavior

Tables run sequentially in declared YAML order. For each table:

1. Print source row count for the slice (with the WHERE).
2. Print local row count before.
3. If `truncate: true`, `TRUNCATE TABLE` locally.
4. `INSERT INTO <table> SELECT * FROM remote(<source>, <table>, user, pw) <WHERE...>`.
5. Print local row count after.

All SQL executes on the local server; the source is reached via `remote()`, or `remoteSecure()` when `CHCOPY_SOURCE_PORT=9440` (ClickHouse's TLS native port). The same heuristic applies to the local connection — port `9440` ⇒ TLS, port `9000` ⇒ plain. Failures abort immediately — no partial-success bookkeeping.

## Safety guards

`chcopy` does not block on the write target — you are responsible for pointing it at the right server. As a soft guard, if `CHCOPY_LOCAL_HOST` does not resolve to localhost or a private / docker-bridge address (`10/8`, `172.16/12`, `192.168/16`), `chcopy` prints a warning and pauses for 10 seconds before proceeding. Ctrl-C aborts.

Required env vars (`CHCOPY_LOCAL_*` and `CHCOPY_SOURCE_*`) must still be set, since without them there is nothing to connect to.

## Notes

Not battle-tested. Review your config and the printed SQL (use `--dry-run`) before running. No warranty of any kind.
