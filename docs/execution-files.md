# Execution files and variables

Project source and execution documents have separate storage. `factory_dir` is a literal filesystem path, defaulting to `.factory`. Relative paths resolve beside `factory.json`; absolute paths are allowed. No shell, environment or tilde expansion is performed.

```json
{
  "factory_dir": ".factory",
  "variables": {
    "worktree": "${execution.factory_dir}/worktrees/${execution.id}",
    "workspace": "${variables.worktree}"
  }
}
```

Each new execution creates `<factory_dir>/executions/<execution-id>/files/` before hooks or agents start. Frozen input copies live in its sibling `inputs/` directory. The execution directory has mode `0700`. Steps, review loops and retries share the same pinned files path; controller restarts and later edits to factory.json do not relocate existing executions. Files remain after success, failure or cancellation. Deletion is an explicit operator action after active processes have stopped.

Add the configured runtime directory to the project's `.gitignore`; for the default, use `.factory/`. Controls does not edit project ignore rules. A dot directory and permissions are not a sandbox: agents running as the host user can access other host files. Container volumes and remote artifact transport are separate runtime capabilities, not provided by this change.

## References

| Reference | Value |
|---|---|
| `${execution.id}` | Stable execution ID |
| `${execution.project}` | Absolute directory containing factory.json; the initial command directory |
| `${execution.factory_dir}` | Absolute configured factory directory |
| `${execution.files_dir}` | Absolute execution files directory |
| `${variables.NAME}` | A declared string variable |

Variable names match `[A-Za-z_][A-Za-z0-9_]*`. Variables may reference other variables; cycles, undefined names, unknown namespaces, unterminated references and NUL bytes fail before execution starts. `$${` emits literal `${`. Ordinary dollar signs are literal. Values are inserted once, never evaluated as shell code or recursively interpreted after insertion. No implicit environment-variable expansion occurs.

References are supported in package `path`, `entrypoint`, `args`, and hook `command`/`arguments` at execution, stage and step boundaries. They are not evaluated inside prompts, routes, output names or opaque package `overrides` settings. `{prompt}` remains the command runner's separate assignment substitution. The literal `factory_dir` cannot depend on execution variables.

`LoadExecution` binds the configuration once with its allocated execution ID. The database stores the bound values, arguments, storage paths and configuration digest. Retries use those records. Do not put secret values in variables: configuration and command arguments are recorded. Existing provider credential references remain supported by their native bundles.

```json
{
  "name": "prepare",
  "command": "python3",
  "arguments": ["/installed/prepare.py", "--files-dir", "${execution.files_dir}"]
}
```

There is no `FACTORY_FILES_DIR` environment variable. Commands receive paths through explicit arguments; hook stdin also contains `files_dir` and the resolved `variables` map. Agent assignments identify the files directory and each required output's absolute path.

## Documents and artifacts

`outputs`, extra file publications and reported file names resolve within the execution files directory, not the project checkout. The host collector checks containment, rejects escaping paths/symlinks, hashes actual bytes and retains published review revisions. A source-checkout copy does not satisfy a missing execution document. Human review opens the collected/published paths.

Agents write source in the workspace chosen by their launcher and documents in the assigned files directory. The native bridge identifies its actual process working directory as the authoritative source workspace; the configuration directory must not be substituted or inferred from document paths. The generic assignment describes this distinction without presenting the configuration path as a source root. Document utilities should receive the assigned absolute input/output paths. Neither agent messages nor action outputs choose or mutate the execution files location.

Source code need not be Git-backed. For an optional Git workflow, factory scripts prepare the project worktree and agent launchers select it. Controls has no Git operation or worktree lifecycle. Execution documents are outside that worktree and are not automatically included in project commits. See [hooks](hooks.md) and the [Registry Go factory](../../registry/factories/golang/README.md).

Host invocations, process output and publication review copies still use service-private runtime storage; status, messages, decisions and accounting remain in the database. `factory_dir` does not move the database or its service configuration. Package snapshots exclude the configured runtime subtree when it falls beneath a package source so new execution files do not invalidate that package's source digest.

For a factory without Git, omit the Git hooks and use the agent's ordinary launch arguments. Execution files and variables still work; no repository, branch or worktree is required. The optional `--workspace` argument belongs to the supplied Go agents, not to the control-plane protocol. Custom agents can consume the same path through whatever explicit argument their entrypoint supports.
