---
name: storage-lock-with-lock
description: Run a child command for its full duration while holding a renewable distributed lease lock, then always release the lock. Use when an agent task/script/deploy/migration must be mutually exclusive from start to finish, not just during one CLI call.
---

# Run Command Under Distributed Lock

Use `storage-lock-cli skills with-lock` when a task can exceed a fixed TTL and must stay locked until it finishes or is interrupted.

## Command

```bash
storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills with-lock --id=deploy --ttl=30s -- ./deploy.sh --commit abc123
```

Flags:

- `--id` lock identifier, required.
- `--ttl` initial lease duration, minimum `3s`; default `30s`.
- `--owner` optional; a unique owner like `with-lock/<uuid>` is generated when omitted.
- `-- COMMAND [ARG...]` required child command.

## Contract

One JSON object is printed to stdout after the child exits:

```json
{"success":true,"status":"executed","lock_id":"deploy","owner_id":"with-lock/...","fencing_token":1,"command":"./deploy.sh"}
```

Statuses:

- `executed`: child exited 0, lock was released.
- `child_exited`: child exited non-zero; the CLI exits with the child's exit code.
- `interrupted`: SIGINT/SIGTERM arrived and the child was cancelled.
- `busy`: another agent holds the lock at acquisition time.
- `lost`: the lease was lost while the child was running.

## Important Rules

- A watchdog renews the lease while the child runs, so the task may last longer than `--ttl`.
- SIGINT/SIGTERM cancels the child, then releases the lock best-effort with an independent timeout.
- On any child exit path, the lock is released via a deferred `unlock`; even a non-zero child exit does not leave the lock held.
- The child inherits stdin/stdout/stderr. Do not put coordination output inside the child's stdout unless the child itself owns that output.