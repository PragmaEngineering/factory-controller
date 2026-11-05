# Local service recovery

These commands work without `factory.json` and do not require a running daemon.

```bash
factory stop          # graceful service shutdown; preserve history and files
factory stop --all    # also stop recorded agents and managed database leftovers
```

`stop --all` first requests normal shutdown, then falls back to verified host
processes. It stops the service, agent process groups, and the embedded database
in that order. Database shutdown checkpoints data; it never deletes a database
lock file. No process is forcibly killed after a timeout: failures are reported.

Ownership records live in `~/.factory/runtime/resources/`, independently of
PostgreSQL. Process records include PID, user, process group, and OS start time;
a reused PID is not sufficient authority to signal a process. Database recovery
checks its executable and exact data directory before invoking its shutdown tool.
Records contain no credentials or command arguments.

For releases predating ownership records, recovery recognizes only the embedded
database path derived from the current service's runtime directory. It does not
scan or stop legacy per-eval services. Unrecorded historical agent processes
cannot be safely identified automatically.

External databases, remote engines, user-installed binaries, source workspaces,
and Git worktrees are not managed cleanup targets. Stopping resources does not
cancel workflows or resume interrupted agents. Inspect execution state before
retrying.

## Prune disposable resources

```bash
factory stop --all
factory prune --dry-run
factory prune
```

Pruning requires the local service and recorded processes to be stopped. It
removes recorded private agent profiles, stale process ownership records, and
an abandoned service socket. Execution evidence, logs, and database data remain.
Dry-run lists candidates without removing them. Unknown files are left alone.

Managed paths are recorded with filesystem identity. Cleanup refuses symlink
redirects and replaced directories rather than inferring ownership from names.
Recursive deletion does not follow links into source workspaces.

## Remove retained data

```bash
factory stop --all
factory prune --all --dry-run
factory prune --all          # lists targets and requires typing yes
factory prune --all --yes    # explicit noninteractive authorization
```

`--all` additionally deletes recorded execution inputs, artifacts, invocation
files, service logs, and managed embedded database data, including execution
history. It does not stop running resources implicitly. A cancelled confirmation
or EOF removes nothing. Dry-run never asks for confirmation or removes resources.

Only journaled paths are deleted. Executions from before ownership tracking may
have unrecorded files; these are preserved. User configurations, source workspaces,
worktrees, shared download caches, and externally managed dependencies are never
prune targets. Ownership records and lifecycle lock files remain so concurrent
clients continue to coordinate safely. A running agent group whose original
leader is no longer identifiable blocks cleanup; inspect it manually.
