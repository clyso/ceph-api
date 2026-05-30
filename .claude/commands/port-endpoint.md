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

`tasks/tasks.md` is a plain checkbox list of `{method} {path}` lines with a status checkbox: `[ ]` = todo, `[x]` = done. Nothing else.
Each task corresponds to a Ceph dashboard API endpoint to migrate into this project, and gets a corresponding file `tasks/{method}-{path}.md` once any implementation attempt is made. The filename is sanitized: lowercase, with every non-alphanumeric char replaced by a dash `-`.

## Hard rules

- **You edit nothing outside `tasks/`.** Forward any fix to the implementer subagent.
- **You don't research, explore, or read source code.** Investigation, implementation, and review all happen in subagents with fresh context. Your only reads are `tasks/tasks.md`, the current task file, and subagent responses; your only actions are `git`, `make` gates, and the mechanical task-file checks in the pipeline. Don't open `api/`, `pkg/`, `test/`, or the Ceph submodule yourself — keep your context lean.
- In `tasks/tasks.md` you may **only** check a line `[x]` or reorder the
  list (e.g. move a skipped line to the end). No other edits.
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
   After it returns, do a mechanical presence check on §Requirements (text only — no code reading): it must contain a request shape, a response shape, the required permission, and the target-service line (existing proto or new service name). If any is missing, resume the investigator to fill the gap.

3. **Implement** — spawn the `endpoint-implementer` agent with the
   task-file path. It writes proto/http.yaml/handler/e2e/parity and must
   leave `make gate` then `make full-gate` green. Agent prompt has all info, only task path needed.
   - If it hits the **novel-logic HARD STOP** (handler needs real Go
     logic / a service layer with no existing analogue), it writes a
     problem statement to §Skip reason and stops. Move the line to the
     end of the list (leave it `[ ]`) and continue to the next endpoint.
     IMPORTANT: check the next endpoints for skip — endpoints are CRUDs grouped by resource, starting from CREATE. If we skip one, we skip the full group for that resource, keeping their initial relative order when moving the group to the end of the list. No need to create task files for the rest of the skipped group.
   - If gates are red, send the failing output back to the **same**
     implementer agent (resume it) to fix; repeat until green or stop pipeline after 3 failed attempts.

4. **Review** — spawn `endpoint-reviewer-mechanical` and
   `endpoint-reviewer-deep` **in parallel** (single message, two Agent
   calls), each with the task-file path and told to review the local
   diff. They return findings to you and you append findings to §Review (ids `M*` / `D*`, each a
   checkbox).

5. **Fix** — if there are findings, resume the **implementer** agent with
   the task-file path to address them (it ticks each box or adds a
   one-line reason for not applying — it is told to push back on
   false positives). Re-run `make gate` / `make full-gate`.
   After it returns, mechanically check §Review (text only): every `M*`/`D*` finding is either ticked `[x]` or has a one-line dismissal reason. If any is unaddressed, resume the implementer.

6. **Final check** — gates green. If `api_diff.yaml` gained entries this
   endpoint, inspect each: is it genuinely endpoint-specific, or a
   shape-class the matcher already coerces / fixable in the proto? Any
   unjustified entry is forwarded to the implementer to fix (you do not
   edit it). Loop back to step 5 until clean.

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

## Shared references (point agents at these; don't inline)

- [anatomy.md](../port-endpoint/anatomy.md) — how an endpoint maps onto every layer.
- [permissions.md](../port-endpoint/permissions.md) — dashboard→Go permission mapping.
- [parity README](../../test/parity/README.md) — the parity gate.
- The `ceph-src` skill — the only source of Ceph facts (never training
  data).

## Logging for later tuning

Keep per-endpoint logging in the task file lightweight but useful for
improving these prompts later: number of implement/fix iterations, any
gate that went red and why, review findings that were false positives,
and any skip (with its reason). Don't add heavy instrumentation.
