---
name: storage-lock-lock
description: Acquire, renew, inspect, or release a distributed lease lock with BUSY/FAILURE semantics via storage-lock-cli. Use when multiple agents must mutually exclude a resource, serialize writes/deploys/migrations, or make only one agent responsible for a discrete critical step.
---

# Distributed Lease Lock

Use `storage-lock-cli skills lock|renew|unlock|status` to coordinate exclusive access to one resource.

## Preconditions

- `storage-lock-cli` is built/installed.
- All agents use the same `--storage` and `--dsn`, so they share one storage backend.
- The storage backend supports CAS (mariadb/mysql/tidb/postgresql/mongodb/sqlserver all do).

## Command Contract

Every invocation prints one JSON object to stdout:

```json
{"success":true,"status":"held","lock_id":"resource-x","owner_id":"agent/...","fencing_token":1,"remaining_lease":"30s"}
```

Branch on `success` and `status`; never parse error text. Possible statuses are `held`, `busy`, `released`, `lost`, `free`, `unlock_failed`.

## Workflow

1. Acquire:
   ```bash
   storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills lock --id=resource-x --ttl=30s
   ```
   - `--owner` is optional; a random unique owner is generated when omitted.
   - Default is try-once: `status: "busy"` means another agent currently holds it.
   - Add `--wait` to block until acquired or `--timeout` expires.

2. Before the lease expires, extend it:
   ```bash
   storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills renew --id=resource-x --owner="$OWNER" --ttl=30s
   ```

3. Release explicitly when done:
   ```bash
   storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills unlock --id=resource-x --owner="$OWNER"
   ```

4. Inspect current state:
   ```bash
   storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills status --id=resource-x
   ```

## Important Rules

- `skills lock` runs without a watchdog. The lease is strictly bounded by `--ttl`; the process exits immediately after acquiring. If the job will outlive `--ttl`, either call `renew` before expiry or use `with-lock`.
- Always keep the returned `owner_id`; you need it for `renew` and `unlock`.
- Before an irreversible side effect, verify `fencing_token` and use it as a monotonic guard. A nonzero token returned by a renewal means the lock is still held by that owner; a zero token means the caller must treat ownership as uncertain and stop.
- Treat `success:false` with `status:"busy"`/`"lost"` as a normal coordination result, not an infrastructure failure.