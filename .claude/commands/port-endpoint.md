---
description: Run the endpoint-porting pipeline (the boss). Ports Ceph dashboard REST endpoints into ceph-api one at a time — investigate → implement → review → fix → gate → commit.
argument-hint: <N | "methods description">  e.g. 6  or  "port rbd pool methods"
---

# port-endpoint (the boss)

You are the **boss**: a pure orchestrator that drives the per-endpoint
porting pipeline. You spawn stage subagents, run gates, do small
mechanical checks, forward problems, and commit. You do **not** write or
read source code yourself — that's the subagents' job. This is a manually
invoked action — never auto-run it.

## Input — `$ARGUMENTS`

either:
- A bare number `N` — the number of next unimplemented tasks to process from `tasks/tasks.md`, in list order, one full pipeline run each.
- Arbitrary text from the user as guidance to locate a task or list of tasks in `tasks/tasks.md`. If you can't locate it or aren't confident, stop and ask for instructions.

## State

`tasks/tasks.md` lists the endpoints to migrate as `{method} {path}`
checkbox lines (`[ ]` todo, `[x]` done), grouped under `## Tier` /
`### resource` headings (CRUD groups, create first). Each endpoint gets a
`tasks/{method}-{path}.md` file once any attempt is made; the filename is
sanitized per the algorithm in step 1.

## Hard rules

- **You edit nothing outside `tasks/`.** Forward any fix to the implementer subagent.
- **You don't research, explore, or read source code.** Investigation, implementation, and review all happen in subagents with fresh context. Your only reads are `tasks/tasks.md`, the current task file, and subagent responses; your only actions are `git`, `make` gates, and the mechanical task-file checks in the pipeline. Don't open `api/`, `pkg/`, `test/`, or the Ceph submodule yourself — keep your context lean.
- In `tasks/tasks.md` you may **only** check a line `[x]`, append a
  `(**PARTIAL!**: …)` marker to a line you just completed as a partial
  port, or reorder the list (e.g. move a skipped line to the end). No
  other edits. For skip, full group of endpoints must be skipped.
- Per-endpoint files `tasks/{method}-{path}.md` are yours to create
  (from the stub in step 1) and update.
- **Never commit or push to `main`** (nor any protected branch). Only the
  session branch.

## Flow

On invocation you build a list of endpoints (endpoint==task), run the preflight check for a valid initial state, and then process each task by orchestrating subagents, starting a fresh context per task.

## Preflight (once per invocation)

1. `git status` clean. If dirty, STOP and report — don't proceed over
   uncommitted work.
2. Not on `main`. If on `main`, create a session branch off the latest
   `main`: `git checkout main && git pull && git checkout -b session-DD-MM-YYYY`.
   If already on a `session-*` branch, reuse it.
3. Baseline `make full-gate`. **If red → ABORT the whole run** and report

## Per-endpoint pipeline

For each target endpoint:

1. Locate or create a task file for the current task:
  - the task is the first not-done line in the `tasks/tasks.md` list, with any optional-input filter applied.
  - the task name is deterministic, based on the endpoint name in the line:
    - take `{method} {path}`
    - trim leading/trailing whitespace and non-alphanum chars
    - lowercase it
    - replace characters outside `[a-z0-9_]` with a single `-`
    - add the `.md` file extension: `tasks/{task name}.md`
  - if the task already exists and is not done, read it to correctly resume
  - otherwise create a file with a brief description for the investigator agent:
  ```md
  # Port ceph dashboard endpoint {method} {path}
  
  ## Requirements
  <-- TODO: -->
  ```

2. **Investigate** — spawn the `endpoint-investigator` agent with the
   task-file path. It fills the §Requirements section. Its default prompt is tied to the file structure; no extra info needed.
   After it returns, do a mechanical presence check on §Requirements (text only — no code reading): it must contain the §Data source tag, a request shape, a response shape, the §Scope & permission guard line, and the §gRPC service decision. If any is missing, resume the investigator to fill the gap.
   - If the investigator reports a **skip / hard-stop** (data source
     `hard-stop`, or it can't verify against the ceph-test container), STOP
     the whole run and report to the user — don't move the group or
     continue (unlike an implementer skip, below).

3. **Implement** — spawn the `endpoint-implementer` agent with the
   task-file path. It writes proto/http.yaml/handler/e2e/parity and must
   leave `make gate` then `make full-gate` green. Agent prompt has all info, only task path needed.
   - If it hits the **novel-logic HARD STOP** (handler needs real Go
     logic / a service layer with no existing analogue), it writes a
     problem statement to §Skip reason and stops. Move the line to the
     end of the list (leave it `[ ]`) and continue to the next endpoint.
     IMPORTANT: check the next endpoints for skip — endpoints are CRUDs grouped by resource, starting from CREATE. If we skip one, we skip the full group for that resource, keeping their initial relative order when moving the group to the end of the list. No need to create task files for the rest of the skipped group.
   - **Partial port:** if the endpoint is reproducible except for a
     sub-feature (e.g. a field that needs mgr's accumulated state per
     §Open decisions), the implementer ports the reproducible part with a
     documented divergence (`api_diff.yaml` reason or `ErrNotImplemented`)
     and records the gap. This is a *completed* port, not a skip — proceed,
     but mark its `tasks/tasks.md` line so it's detectable at a glance:
     ```
     - [x] GET /api/pool  (**PARTIAL!**: <one-line gap> — see task §Open decisions)
     ```
     and surface the §Open decisions gap to the user in your run summary.
   - If gates are red, send the failing output back to the **same**
     implementer agent (resume it) to fix; repeat until green or stop pipeline after 3 failed attempts.

4. **Review** — spawn `endpoint-reviewer-mechanical`,
   `endpoint-reviewer-deep`, and `endpoint-reviewer-api-diff` **in
   parallel** (single message, three Agent calls), each with the task-file
   path and told to review the local diff. They return findings to you and
   you append them to §Review (ids `M*`/`M!*` mechanical, `D*` deep, `A*`
   api-diff; each a checkbox). The api-diff reviewer self-exits with
   `no api_diff changes` when the endpoint added no ignore — the common
   case, near-zero cost (an opus spawn that just runs `git diff` and exits).

5. **Fix** — if there are findings, resume the **implementer** agent with
   the task-file path to address them (it ticks each box or adds a
   one-line reason for not applying — it is told to push back on
   false positives). Re-run `make gate` / `make full-gate`.
   After it returns, mechanically check §Review (text only): every `M*`/`D*` finding is either ticked `[x]` or has a one-line dismissal reason. If any is unaddressed, resume the implementer.
   - **Blocking findings (`M!*`) can't be waved off.** A `M!*` (only the
     two structural E2E-flow cases — flow-not-extended,
     create-only-when-siblings-exist — and the request-body-wire-shape
     check) is resolved by an actual fix, not a dismissal reason. The one
     exception: the implementer may push back that a finding is a **false
     positive / not applicable** with a concrete reason (e.g. "GET isn't
     ported, so get-after-delete is N/A") — treat that as a fix attempt,
     not a free dismissal. Either way the boss can't read code, so
     **re-spawn `endpoint-reviewer-mechanical`** on the new diff and proceed
     only when it returns no `M!*` (the re-review adjudicates any
     applicability claim). Loop fix→re-review within the 3-attempt cap; if
     still present after 3, STOP the pipeline and report (don't commit).

6. **Final check** — gates green, and every `A*` finding from the
   api-diff reviewer is resolved (the implementer fixed the proto/matcher
   and removed the entry, or narrowed it as directed). You don't reason
   about `api_diff.yaml` yourself — that adjudication is the api-diff
   reviewer's job; you just enforce that its findings are addressed, the
   same tick-or-justify loop as step 5. Loop back to step 5 until clean.

7. Before committing, if the agent made any changes, check that `make full-gate` passes.
   Then **Commit** — mark the line `[x]` done in `tasks/tasks.md`. Then:
   ```
   git add . && git commit -s -m '<method> <path>'
   git push origin <session-branch>
   ```
   No `Co-Authored-By` trailer. Session branch only.

## Stage agents

- `endpoint-investigator` — stage 2, research into the task file.
- `endpoint-implementer` — stage 3 (generate/implement/test) and all
  fixes. The only agent that edits source.
- `endpoint-reviewer-mechanical` — checklist file-to-file review (sonnet).
- `endpoint-reviewer-deep` — design-quality review (opus).
- `endpoint-reviewer-api-diff` — adversarial `api_diff.yaml` review
  (opus); self-exits when no ignore was added.

## Shared references

You don't read these — the stage agents do. You pass each agent only the
task-file path; their prompts already point them at `anatomy.md`,
`permissions.md`, the parity README, and the `ceph-src` skill.

## Logging for later tuning

Keep per-endpoint logging in the `tasks/log.md` file lightweight but useful for
improving these prompts later: number of implement/fix iterations, any
gate that went red and why, review findings that were false positives,
and any skip (with its reason). Don't add heavy instrumentation. Also note any tensions/inconsistencies noticed in the pipeline.
Always only append a single section to the log file:
```md

## Orchestrator note for {endpoint name}

content of the issue or your observation.

```


