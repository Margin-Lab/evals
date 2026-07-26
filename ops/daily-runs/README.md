# Daily tracker operations

This directory is the version-controlled source for MarginLab's daily Codex and
Claude Code tracker runner. It contains orchestration and statistics code only.
Run outputs, logs, credentials, agent configurations, eval configurations,
warm-up scripts, and caches remain local operational state and must not be
committed.

The currently installed cron job still executes the external copy at:

```text
/home/jbouza/Projects/marginlab-eval/daily-runs/run-from-cron.sh
```

Do not switch cron to this copy until the pull request containing this
directory is merged and the production `Margin-Lab/evals` checkout has
fast-forwarded to that merge.

## Runtime layout

The scripts deliberately separate versioned code from local state. When run
from the repository, set `MARGINLAB_EVAL_WORKSPACE_ROOT` to the existing
workspace:

```sh
export MARGINLAB_EVAL_WORKSPACE_ROOT=/home/jbouza/Projects/marginlab-eval
```

When the repository is installed at the documented production path
`<workspace>/evals/ops/daily-runs`, the default is resolved consistently as
three parent directories above the scripts. Other checkout layouts must set
`MARGINLAB_EVAL_WORKSPACE_ROOT` explicitly.

That makes the scripts use the existing untracked state under
`$MARGINLAB_EVAL_WORKSPACE_ROOT/daily-runs`, including `runs/`,
`agent-configs/`, `eval-config*.toml`, and `warmup-claude.sh`.

Retries are resumable at the sync boundary. If exactly one completed
`results.json` and `statistics.json` pair already exists for a tracker and
target date, the runner skips the paid evaluation, reruns that tracker's raw
data sync, and lets the parent runner continue to publication. More than one
completed run for the date fails closed for operator reconciliation.

“Completed” is validated from content, not file presence: `results.state` must
be `completed`, the suite size must match, and the statistics target date and
failure policy must match the current invocation. Failed, stale, or
unnormalized legacy pairs are not reused. The versioned `sync-run.sh` copies
the selected pair directly from `MARGINLAB_DAILY_STATE_DIR` to
`MARGINLAB_RAW_REPOSITORY`, so location overrides apply through the entire
retry path.

Statistics generation applies the same sample contract before aggregation.
Failed runs and prior completed runs with the wrong suite size, inconsistent
status totals, or too few policy-valid instances are excluded. The current
run's `results.json` is explicitly required and must pass those checks, so a
bad new run cannot be hidden by valid history or publish stale statistics.

### Hash-pinned legacy normalization

Statistics created before the versioned runner did not record
`non_test_failure_policy`. The old calculator always used
`succeeded + test_failed` as its denominator, which is the current `exclude`
policy. A legacy file may be upgraded without rerunning its paid evaluation,
but only through `statistics/normalize_legacy_policy.py`.

The normalizer does not make a general “missing means exclude” assumption. Its
checked-in allowlist contains the exact SHA-256 hashes of the two reviewed
July 25 results, legacy statistics, and normalized outputs. It validates the
completed run, suite size, status totals, target date, daily history, and daily
timescale; creates an exact backup; validates the normalized candidate with the
normal strict validator; and atomically replaces the statistics file. A second
invocation is an idempotent no-op only when both the normalized file and its
legacy backup match their expected hashes.

```sh
python3 statistics/normalize_legacy_policy.py \
  --migration codex-20260725-040001 \
  --results <run>/results.json \
  --statistics <run>/statistics.json \
  --backup <backup-dir>/codex-statistics.json

python3 statistics/normalize_legacy_policy.py \
  --migration claude-code-20260725-045635 \
  --results <run>/results.json \
  --statistics <run>/statistics.json \
  --backup <backup-dir>/claude-code-statistics.json
```

After normalization, the unchanged strict retry path can reuse and sync the
run. The publisher remains strict and will never export an implicit policy.

## Site-data publication

The parent runner always invokes the website's site-data publisher after both
trackers pass. With no flag, this is a read-only export and validation in a
temporary directory. It does not fetch, commit, push, open a pull request, or
deploy Firebase. The runner removes any inherited
`MARGINLAB_SITE_DATA_PUBLISH` value in this mode.

`./run.sh --publish` and `./run-from-cron.sh --publish` enable Git publication.
The runner then supplies both confirmations required by the publisher:
`--publish` and `MARGINLAB_SITE_DATA_PUBLISH=1`. After validating one immutable
snapshot, the publisher stages only `site-data/**` in a temporary detached
worktree, creates one descendant commit, and pushes that exact commit directly
to `origin/main`. Immediately before the normal non-force push, it refetches
and requires remote `main` to still equal the captured base; concurrent
movement or a non-fast-forward update fails closed.

The dedicated automation clone must be clean, on `main`, have canonical
`Margin-Lab/marginlab` as its origin, and be explicitly marked:

```sh
git -C /home/jbouza/Projects/marginlab-site-data-automation \
  config --local marginlab.siteDataAutomation true
```

A retry with an already-published payload is a deterministic no-op. The
temporary worktree is removed even after failure, and the dedicated clone is
returned clean on an ff-only synchronized `main`; no retry branch or local
publication commit needs reconciliation. If that final ff-only restoration
cannot reconcile, publication fails for operator review. Publication does not
run Firebase itself; the push starts the website's normal `main` checks and
staging workflow, while production deployment remains separately approved.

Optional overrides are:

| Variable | Default |
| --- | --- |
| `MARGINLAB_DAILY_STATE_DIR` | `$MARGINLAB_EVAL_WORKSPACE_ROOT/daily-runs` |
| `MARGINLAB_SWE_SUITES_ROOT` | `$MARGINLAB_EVAL_WORKSPACE_ROOT/swe-suites` |
| `MARGINLAB_PROJECTS_ROOT` | Parent of the eval workspace |
| `MARGINLAB_RAW_REPOSITORY` | `$MARGINLAB_PROJECTS_ROOT/marginlab` |
| `MARGINLAB_EVALUATION_ROOT` | `$MARGINLAB_EVAL_WORKSPACE_ROOT/evals` |
| `MARGINLAB_AUTOMATION_ROOT` | `$MARGINLAB_PROJECTS_ROOT/marginlab-site-data-automation` |
| `MARGINLAB_DAILY_LOG_DIR` | `$MARGINLAB_DAILY_STATE_DIR/logs` |
| `MARGINLAB_DAILY_LOCK_FILE` | `/tmp/marginlab-daily-runs.lock` |
| `MARGINLAB_DAILY_TIMEOUT_SECONDS` | `28800` |
| `MARGINLAB_DAILY_ALERT_HOOK` | Empty; no hook |
| `MARGINLAB_EXPECTED_INSTANCES` | `50` |
| `MARGINLAB_MINIMUM_VALID_INSTANCES` | Expected instance count |
| `MARGINLAB_NON_TEST_FAILURE_POLICY` | `exclude` |

The alert hook, when configured, must be an executable absolute path. It
receives `daily-run-failed`, the exit status, and the absolute log path.

## Verify

This command performs syntax and unit checks only; it does not run an
evaluation, publish data, or deploy the site:

```sh
./ops/daily-runs/test.sh
```

Before activation, also confirm the production checkout is clean and points at
the merged commit:

```sh
git -C /home/jbouza/Projects/marginlab-eval/evals status --short
git -C /home/jbouza/Projects/marginlab-eval/evals log -1 --oneline
```

## Atomic activation after merge

First fast-forward the production checkout and run the safe checks:

```sh
git -C /home/jbouza/Projects/marginlab-eval/evals fetch origin main
git -C /home/jbouza/Projects/marginlab-eval/evals merge \
  --ff-only origin/main
/home/jbouza/Projects/marginlab-eval/evals/ops/daily-runs/test.sh
```

Then create a candidate crontab from the installed one. The replacement keeps
the existing schedule and adds no `--publish` flag, so the first scheduled run
still performs a publication dry run:

```sh
cron_backup="$(mktemp /tmp/marginlab-crontab.backup.XXXXXX)"
cron_candidate="$(mktemp /tmp/marginlab-crontab.candidate.XXXXXX)"
old_runner="/home/jbouza/Projects/marginlab-eval/daily-runs/run-from-cron.sh"
new_runner="env MARGINLAB_EVAL_WORKSPACE_ROOT=/home/jbouza/Projects/marginlab-eval /home/jbouza/Projects/marginlab-eval/evals/ops/daily-runs/run-from-cron.sh"

crontab -l >"${cron_backup}"
test "$(grep -Fc "${old_runner}" "${cron_backup}")" -eq 1
sed "s|${old_runner}|${new_runner}|" \
  "${cron_backup}" >"${cron_candidate}"
grep -F "${new_runner}" "${cron_candidate}"
diff -u "${cron_backup}" "${cron_candidate}"
crontab "${cron_candidate}"
```

`crontab <file>` installs the complete candidate in one operation, avoiding a
partially edited schedule. Keep `cron_backup` until the first scheduled dry run
has succeeded. Roll back atomically with:

```sh
crontab "${cron_backup}"
```

After the first scheduled run, verify its dated log under
`/home/jbouza/Projects/marginlab-eval/daily-runs/logs`. Confirm that it reports
a successful dry run, and that the automation clone stayed clean on `main`
without creating or pushing a commit:

```sh
git -C /home/jbouza/Projects/marginlab-site-data-automation status --short
git -C /home/jbouza/Projects/marginlab-site-data-automation branch --show-current
git -C /home/jbouza/Projects/marginlab-site-data-automation \
  config --local --get marginlab.siteDataAutomation
git -C /home/jbouza/Projects/marginlab-site-data-automation \
  remote get-url origin
```

Only after that dry run and a review confirming the site-data validation and
website `main` checks are ready should a second atomic crontab edit enable
direct Git publication:

```sh
publish_backup="$(mktemp /tmp/marginlab-crontab.pre-publish.XXXXXX)"
publish_candidate="$(mktemp /tmp/marginlab-crontab.publish.XXXXXX)"
dry_runner="env MARGINLAB_EVAL_WORKSPACE_ROOT=/home/jbouza/Projects/marginlab-eval /home/jbouza/Projects/marginlab-eval/evals/ops/daily-runs/run-from-cron.sh"
publish_runner="${dry_runner} --publish"

crontab -l >"${publish_backup}"
test "$(grep -Fc "${dry_runner}" "${publish_backup}")" -eq 1
sed "s|${dry_runner}$|${publish_runner}|" \
  "${publish_backup}" >"${publish_candidate}"
grep -F "${publish_runner}" "${publish_candidate}"
diff -u "${publish_backup}" "${publish_candidate}"
crontab "${publish_candidate}"
```

Keep `publish_backup` through the first publication. Roll back to validation
only with `crontab "${publish_backup}"`. After the first publish-mode run,
confirm the log reports either the exact publication commit or a deterministic
no-op, the automation clone is clean on `main`, and `origin/main` contains that
commit. Also confirm the website's normal `main` checks started; this runner
does not approve or perform a production Firebase deployment.

The old external scripts can remain as a rollback copy until the new runner
has completed multiple scheduled dry runs. They are no longer the source of
truth after activation.
