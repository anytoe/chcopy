# chcopy example

A small end-to-end walkthrough: two ClickHouse instances running in Docker — a "source" seeded with sample data and an empty "local" — and a YAML config that copies a curated slice from source to local using `chcopy`.

## Prerequisites

- `chcopy` installed (`go install github.com/anytoe/chcopy@latest` or `go build` from the repo root)
- Docker
- Ports `8123`, `8124`, `9000`, `9001` free on localhost

## 1. Start both ClickHouse instances

```sh
cd examples
docker compose up -d
```

This starts two containers on a shared Docker network:

- `chcopy-example-source-server` — the "source" (PRD analogue). Seeded with `initial-schema.sql` and `seed-data.sql` (database `shop` with `users` and `orders`, populated with sample rows). Reachable from your host on `localhost:9000`.
- `chcopy-example-local-server` — the "local" target. Schema only, no data. Reachable from your host on `localhost:9001`.

The local server reaches the source over the Docker network at the hostname `source-clickhouse:9000`.

Wait a few seconds for both servers to come up, then sanity check:

```sh
# Source has data
docker compose exec source-clickhouse clickhouse-client \
  --user default --password default_password \
  --query "SELECT count() FROM shop.users"
# 4

docker compose exec source-clickhouse clickhouse-client \
  --user default --password default_password \
  --query "SELECT count() FROM shop.orders"
# 6

# Local has the schema but no data
docker compose exec local-clickhouse clickhouse-client \
  --user default --password default_password \
  --query "SELECT count() FROM shop.users"
# 0

docker compose exec local-clickhouse clickhouse-client \
  --user default --password default_password \
  --query "SELECT count() FROM shop.orders"
# 0
```

## 2. Export environment variables

`chcopy` connects to the local server using `CHCOPY_LOCAL_*` and embeds the source connection (used inside `remote(...)` calls executed by local) from `CHCOPY_SOURCE_*`.

```sh
export CHCOPY_LOCAL_HOST=localhost
export CHCOPY_LOCAL_PORT=9001
export CHCOPY_LOCAL_USER=default
export CHCOPY_LOCAL_PASSWORD=default_password

export CHCOPY_SOURCE_HOST=source-clickhouse
export CHCOPY_SOURCE_PORT=9000
export CHCOPY_SOURCE_USER=default
export CHCOPY_SOURCE_PASSWORD=default_password
```

`CHCOPY_SOURCE_HOST` is the hostname *as seen from inside the local container* — i.e. the Docker network alias of the source service. The chcopy CLI does not connect to source directly; every source read happens via `remote(...)` executed by local.

## 3. Inspect the YAML config

```sh
cat config.yaml
```

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

  - name: batch_import
    tables:
      - table: shop.events
        where: "WHERE year >= 2024"
        batch: "year"
        truncate: true
```

`dev_import` has two tables: `users` is copied in full, `orders` is filtered to rows from May 2026 onwards. Both are truncated locally before insert.

`batch_import` shows batching: `shop.events` is filtered to `year >= 2024`, then copied one `INSERT ... SELECT` per distinct `year` (2024, then 2025 — 2023 is excluded by the WHERE). Truncate runs once, before the batches.

List available configs:

```sh
chcopy --config config.yaml --list
# dev_import
# batch_import
```

## 4. Dry-run

```sh
chcopy --config config.yaml --name dev_import --dry-run
```

Prints the SQL `chcopy` would execute, without running it. Use this to review the plan before any writes.

## 5. Run the import

```sh
chcopy --config config.yaml --name dev_import
```

Expected output (row counts before and after each table):

```
shop.users:  source=4, local before=0, truncated, local after=4
shop.orders: source=3, local before=0, truncated, local after=3
```

`shop.orders` shows `source=3` because the source row count is reported *for the slice* (the WHERE), not the whole table — there are 6 rows in source, but only 3 satisfy `created_at >= '2026-05-01'`.

### Batched import

```sh
chcopy --config config.yaml --name batch_import --dry-run
```

For a batched table, dry-run prints the batch-resolution query plus a single templated INSERT (the real `year` values are only known once it connects):

```sql
-- shop.events  (batched by year)
TRUNCATE TABLE shop.events;
-- resolve batches:
SELECT DISTINCT year FROM remote(...) WHERE year >= 2024 ORDER BY year ASC;
-- then one INSERT per batch value, e.g. first batch:
INSERT INTO shop.events SELECT * FROM remote(...) WHERE year >= 2024 AND year = <year>;
```

Running it copies 5 rows (2 from 2024, 3 from 2025) over two batches:

```
shop.events: source=5, local before=0, truncated, batches=2, local after=5
```

## 6. Verify

```sh
docker compose exec local-clickhouse clickhouse-client \
  --user default --password default_password \
  --query "SELECT count() FROM shop.users"
# 4

docker compose exec local-clickhouse clickhouse-client \
  --user default --password default_password \
  --query "SELECT id, created_at FROM shop.orders ORDER BY id"
# 4    2026-05-03 16:45:00
# 5    2026-05-08 18:00:00
# 6    2026-05-12 20:00:00
```

## Cleanup

```sh
docker compose down -v
```

## Files

| File | Purpose |
|---|---|
| `docker-compose.yml` | Spins up the source + local ClickHouse containers |
| `initial-schema.sql` | Seeded into both containers at startup |
| `seed-data.sql` | Seeded into the source only — sample rows in `shop.users` and `shop.orders` |
| `config.yaml` | `chcopy` named-import configuration |
