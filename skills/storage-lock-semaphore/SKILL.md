---
name: storage-lock-semaphore
description: Acquire or release a fixed-capacity distributed semaphore permit via storage-lock-cli. Use when N agents may run concurrently but no more than N, for quotas such as limited database connections, parallel job slots, or preview environment capacity.
---

# Distributed Semaphore

Use `storage-lock-cli skills semaphore` and `semaphore-release` to limit concurrent agents/workers to a fixed capacity.

## Acquire a Permit

```bash
storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills semaphore --id=deploy-slots --capacity=2 --ttl=30s --owner=worker-a
```

Success:

```json
{"success":true,"status":"permit_acquired","lock_id":"deploy-slots","owner_id":"worker-a/1/permit/...","fencing_token":1,"slot_id":"deploy-slots/permit/1","capacity":2}
```

Saturated:

```json
{"success":false,"status":"no_permit","lock_id":"deploy-slots","message":"semaphore saturated","error":"no semaphore permit available"}
```

`--capacity` is a deployment contract: all agents in the same semaphore must use the same value.

## Keep and Release a Permit

- `--hold=5m` keeps the permit for that duration while the lease supervisor renews it, then releases it.
- Without `--hold`, the permit is released when the CLI exits; `--ttl` is the maximum logical hold time.
- To release another agent's currently held permit:
  1. Find the occupied slot with `skills status --id=<namespace>/permit/0`, etc. (slots are `0..capacity-1`).
  2. Extract `owner_id` from the status JSON.
  3. Release:
     ```bash
     storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills semaphore-release --slot=deploy-slots/permit/0 --owner="$OWNER"
     ```
- `semaphore-release` is idempotent: releasing an already-released/already-missing slot reports `released` with `message:"already released"`.

## Important Rules

- A semaphore has more than one permit, so its fencing tokens are per-slot, not global. Use them only to guard work against the exact slot/owner.
- If a holder's process dies, the lease expires and the slot becomes available again; there is no need to force-clean leases.
- `no_permit` is a normal scheduling result: retry later or choose another strategy.