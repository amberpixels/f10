# Step · demo - what a shipment does, and evidence of it

Role: a **senior engineer** showing a change to someone who did not write it.
Input: a shipment (the task, its plan, the diff), usually an open PR or a branch.
Output: **what the change does**, in plain words, and a **demo script** that confirms it; when
executed, the evidence: screenshots and a `report.html` under `<storage root>/demo/<TASK-ID>/`.
Context: the `demo` overlay (`.f10/instructions/demo.md`): how to launch, seed and drive the
app. There is no inferred default executor.

No bug hunt, no verdict; the merge is the user's call. **Static** (default) runs the script and
captures evidence. **Hands-on** (`--hands-on`, or a pipeline entry naming it) seeds the state,
leaves the app running and hands the script over. Everything before execution is the same, and
a static report carries the script and the live url too.

1. **Gather the change** as `steps/judge.md` point 1 does (a PR number or url, the current
   branch, a task id and the branch or PR it points to, no argument): the diff via the host CLI
   or `git diff <base>...HEAD`, plus the task and the plan the branch points to.
2. **Say what changed, in the user's words**: the report's headline. What the app did before,
   what it does now, what a user will notice; two or three sentences, derived from the diff, no
   handler names, component names or file paths. If nothing is visible from outside, say so.
3. **Derive the demo script**: entry point, needed state, ordered steps, what to look for at
   each. The task's **claims** say which effects matter, the diff says which exist; where they
   disagree, follow the diff and report the difference.
4. **Pick the executor** from the `demo` overlay, never inferred: how to launch the app, seed
   baseline state, the base url, test credentials. A project skill that launches and drives the
   app, declared there, *is* the executor.

   **No overlay is a gap to fill** (`conventions/gaps.md`). Look first, in a few targeted
   `Read`/`Grep` calls in one message, then propose:

   - **what drives it** - a Playwright, Cypress, Selenium or Capybara config, an e2e suite and
     its command, a browser MCP server this session has, a project skill that drives the app.
   - **how to launch it** - the dev-server recipe in the `justfile`, `Makefile`, `package.json`
     scripts or `Procfile`, and its port.
   - **how to create what a script needs** (when no existing record fits) - the test suite's
     console, runner or factory, the app's own forms, a seed task. Never ask about reuse or
     cleanup.
   - **how to authenticate** - the test user, the dev-login route, or the header or marker the
     project's CLAUDE.md documents.

   Then **one** `AskUserQuestion`: a question per fact, your finding first (the conservative
   choice where you found nothing), plus **where the report should go** (point 10). Never
   invent a declined answer. Show the overlay, write `.f10/instructions/demo.md`, and continue
   the run; later runs never ask again. If the user declines, the run **degrades to hands-on**,
   naming the missing fact: a normal end.
5. **Set the state up, out loud.** Reuse a record that already fits. Otherwise **create** the
   delta through the overlay's mechanism, never an improvised database route, never in a
   database the overlay did not name. **Say what you will create first.** Created records stay:
   the report names each with its id, and removal is the user's call.
6. **Execute, or hand over.** Static: run the script, capture as you go, note anything that
   behaved unlike the script. Hands-on: seed, leave the app running, print the script with urls
   filled in.
7. **Before and after** only when the change modifies something visible that **already
   existed**: same script, same seeded state, each side's commit named. Otherwise skip and say
   why in one line (a new surface has no before but an empty page or a 404; a migration cannot hold the same state on both
   sides).
8. **One artifact per user-visible claim**, never a target count. A button that grew and
   changed colour is one shot; five screens earn more. A hands-on scenario is the shortest path
   touching every claim, a few minutes at most.
9. **Claim only what you captured.** Every behaviour sentence points at an artifact this run
   produced; list what could not be demonstrated, with why. Never present behaviour read from
   the diff as observed; point 2 is the only diff-derived statement, phrased as what the change
   *does*.
10. **Report, in two places.** In chat, per `conventions/report.md`: the task, the PR url, and
    **only the artifacts this run wrote** (no capture, no `evidence` row; no page, no `report`
    row). Then prose, in order: what changed, what was demonstrated, what was not and why, what
    the run created and left, the script. Close with `f10 demo open <TASK-ID>` when there is a
    file.

    On disk, `report.html` in the evidence directory, in **both** modes: what changed first;
    static, each shot under its claim, before and after side by side; hands-on, the script as a
    checklist. Self-contained: inline styles, images by relative path, legible light and dark,
    no build step or server.

    **Where the page goes is the project's choice** from point 4, recorded in the overlay: local
    file, also published, or published only. `--publish` and `--local` override it for one run.
    Publishing makes a **private page on the user's own account**, but it leaves the machine:
    publish only what the overlay declares or the user asked for in as many words, with **no
    f10 traces** whatever the visibility (`conventions/context.md`): no storage-root paths, no
    plan references, nothing naming the pipeline. Upload screenshots with the page and use the
    urls the host returns. Nothing goes to the PR or the tracker.

**On failure:** the app will not start, or the needed state cannot be reached: stop and report,
saying whether setup created anything. A script that ran while the feature failed to appear is
**not** a failure: report it as not demonstrated; what to do about it is the user's call.
