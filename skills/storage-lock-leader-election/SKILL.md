---
name: storage-lock-leader-election
description: Elect exactly one leader among concurrent agents and optionally hold leadership for a fixed duration via storage-lock-cli. Use when one agent must coordinate/own a batch or background job while other agents follow, wait, or retry.
---

# Distributed Leader Election

Use `storage-lock-cli skills leader-election` when the goal is "one leader, everyone else is a follower", not mutual exclusion of every write.

## One-Shot Campaign

Try once and exit. At most one process is leader at any instant, and the
winner resigns immediately on exit — so concurrent callers can *relay*:
the lock holder resigns, the next caller then wins too. Each report carries
a distinct, strictly increasing `fencing_token`, which is how you tell relay
apart from a double-hold.

```bash
storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills leader-election --id=job-x --ttl=30s --owner=worker-a
```

Success:

```json
{"success":true,"status":"leader","lock_id":"job-x","owner_id":"worker-a/1/leader/...","fencing_token":1}
```

Failure (another leader already holds):

```json
{"success":false,"status":"follower","lock_id":"job-x","error":"lock busy"}
```

## Hold Leadership

Campaign until leader (blocking — callers queue, they do not take turns being
rejected), renew the lease for the whole hold duration, then resign:

```bash
storage-lock-cli --storage=mariadb --dsn="$STORAGE_DSN" skills leader-election --id=job-x --ttl=30s --hold=5m
```

Possible statuses: `leader`, `follower`, `lost`.

## Important Rules

- Follower result has `success:false` so shell-based agents can use exit code branching; do not log it as an error.
- `--hold` uses the coordination package's supervisor, so leadership does not expire as long as the process lives.
- The CLI always resigns best-effort on exit. If the process dies, the lease expires naturally and another agent can become leader after at most `--ttl`.
- Use `fencing_token` to distinguish a real leadership epoch when workers report results or write shared state.
- Use `lock`/`with-lock` for a single critical resource; leader election is for one coordinator plus followers.
- `--hold` blocks and waits its turn: if a leader is already holding, your process waits instead of exiting. If you want "become leader or give up now", use one-shot and branch on `status`.