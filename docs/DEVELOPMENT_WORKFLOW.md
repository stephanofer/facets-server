# Development latency review and working rules

## Evidence from the block 2 session

Reviewed the OpenCode 2.2.2 session message timeline through the documented local
API and the server log at `~/.local/share/opencode/log/opencode.log`. The analysis
window ends at the first delivery response, excluding subsequent troubleshooting.
Raw messages, prompts, provider payloads and logs remain outside this repository.

| Observation | Recorded result |
| --- | --- |
| Implementation window, local UTC-05:00 | 2026-10-29 19:09:50.991–20:40:58.224 |
| Wall time | 91 minutes 7 seconds |
| Assistant/model responses | 128 |
| Tool calls | 198, including 90 shell calls and 46 patch calls |
| Full validation launches | 5; four stopped early, one completed |
| Format launches | 11 through the lint executable |
| Separate integration launches | 4, in addition to the final validation |
| Skills loaded | 13, totaling approximately 132,294 characters |
| Largest model-call input including cache | 291,151 tokens |
| Final full validation | Approximately 3 minutes, 20:33:29–20:36:31 |

The message timestamps account for approximately 43.9 minutes between creation
of assistant responses and their first tool invocation (or completion for the
delivery response). This includes model/provider processing and request generation;
it is **not** a measurement of model CPU time or proof of provider throttling.
The union of recorded tool-call intervals is approximately 44.6 minutes. Actual
background process execution can overlap both categories; do not add background
command durations to these figures as if everything ran sequentially.

Several commands were unusually slow, including two initial lint runs around
124/153 seconds, two fuzz calls reaching their 120-second tool timeout and one
read of a tiny file taking approximately 121 seconds. Those timings show latency,
not its root cause: the available records do not distinguish filesystem/antivirus,
tool scheduling and process contention. No system-wide exclusions or tuning are
justified by that evidence alone.

The server log contains a subscription-sharing quota error at 20:44:30, **after**
the implementation window. It cannot explain the preceding 91 minutes. Warnings
about media resolution/another local path are also insufficient to blame provider
limits, Docker, Windows or PowerShell for the whole session.

## Correctable causes

1. The implementation used too many small edit/tool/model cycles with a growing
   context. Required design review remains necessary; repeated broad context and
   speculative work are not a substitute for focused increments.
2. Full validation started before the source was stable. Continuing to edit during
   a background run led to avoidable format/lint failures and repeated expensive
   checks. Goose's SQL directory initially contained embedding Go code, producing
   another preventable validation failure.
3. Formatting unnecessarily invoked `go tool golangci-lint fmt`, rather than the
   pinned `gofmt` binary. Cold tool builds and repeated startup added overhead.
4. Foreground timeouts and concurrent resource-heavy checks complicated diagnosis.
   A long-running legitimate check must not trigger a duplicate run simply because
   a tool call stopped waiting for it.

These are workflow corrections, not grounds for dropping PostgreSQL integration,
race detection, vulnerability checks or failure-path tests. There is no defensible
promise that a future feature of arbitrary scope will finish in a fixed time.

## Applied changes

- `AGENTS.md` now requires focused increments, restrained context loading, batched
  edits, serial heavy checks and a stable-source final gate. It preserves complete
  required document review and the no-delegation rule.
- `scripts/dev.ps1 format` invokes the selected Go toolchain's `gofmt` directly.
- `check` checks formatting and runs cache-enabled unit tests for local feedback:

  ```powershell
  ./scripts/dev.ps1 check -Packages ./internal/config
  ./scripts/dev.ps1 check
  ```

  It is explicitly **not** a closure gate. `test` can also select packages but
  continues to use `-count=1`; full validation still covers every package.
- Each checked command and the complete task report elapsed time, making a slow
  step visible rather than treating the entire session as one opaque delay.
- Full validation fails on cheap code/test/lint errors before expensive integrity,
  generation, container, race and vulnerability steps. No existing check was removed.
- An exclusive file handle prevents simultaneous `validate` processes in the same
  checkout. No stale-lock deletion is necessary: process exit releases the handle.
- A content fingerprint checks that source, generation inputs, CI scripts and
  runtime/tool configuration have not changed during validation. A mixed-source
  run cannot be reported as a successful final gate.

No global OpenCode configuration, model, reasoning setting, permission, database,
service process or user-wide instruction file was changed. Project-local workflow
changes address the demonstrated mistakes without affecting unrelated projects.

## Verification of this adjustment

- Focused `check`: passed in 2.50 seconds; formatter check 0.31 seconds.
- All-package `check`: passed in 5.72 seconds; formatter check 0.04 seconds.
- Two-process validation exclusion: the second process was rejected before any
  Go/Docker checks.
- Full validation attempt: stopped in 1.84 seconds because Docker's Linux engine
  pipe was unavailable. No full-pass claim is made for this workflow adjustment;
  rerun once Docker is intentionally available. The previous block 2 successful
  validation remains historical evidence, not a replacement for checking changes.

These are warm/current-host measurements, not an apples-to-apples benchmark against
the earlier cold runs. The largest expected improvement comes from fewer failed
validation cycles and less unnecessary work, not from a claimed fixed speedup.
