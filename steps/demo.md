# Step · demo - what a shipment does, and evidence of it

Role: a **senior engineer** showing a change to someone who did not write it.
Input: a shipment (the task, its plan, the diff), usually an open PR or a branch.
Output: **what the change does**, in plain words, and a **demo script** that confirms it.
When the script is executed, the output also holds the evidence: screenshots and a `report.html` under `<storage root>/demo/<TASK-ID>/`.
Context: the `demo` overlay (`.f10/instructions/demo.md`), which says how to launch, seed and drive the app.
There is no inferred default executor, so point 4 asks for an overlay when there is none.

Demo hunts no bugs and gives no verdict, because the merge is the user's call.
It runs in one of two modes:

- **Static** is the default: demo runs the script itself and captures the evidence.
- **Hands-on** is chosen by `--hands-on`, or by a pipeline entry that names it: demo seeds the state, leaves the app running and hands the script over.

Both modes do the same work up to execution.
A static report also carries the script and the live url.

1. **Gather the change.**
   Run `f10 pr diff [<subject>]` and `f10 pr view [<subject>]`.
   The subject is a PR number or url, a task id, or nothing for the current branch.
   These commands find the branch, its open PR and its base.
   Then read the task and the plan the branch points to.
2. **Say what changed, in the user's words.**
   This becomes the report's headline.
   Say what the app did before, what it does now, and what a user will notice, in two or three sentences.
   Derive them from the diff, and leave out handler names, component names and file paths.
   If nothing is visible from outside, say so.

   **In a ship flow**, a change with nothing to show ends the step here, as a normal end.
   Print one line, `demo: skipped - <why>`.
   A change has something to show when it includes any of these:
   - a UI change
   - a change in what the CLI prints or accepts
   - an API response
   - a file the app writes for its user

   A change to tests, prose, CI or internals only is the usual case of nothing to show.
   A `Kind: refactor` plan always skips.
   This check comes before point 4, so a skipped demo asks for no overlay and creates nothing.
   If you are in doubt, demo runs.
   Standalone `/f10:demo` never skips.
3. **Derive the demo script**: the entry point, the state it needs, the ordered steps, and what to look for at each step.
   The task's **claims** say which effects matter, and the diff says which effects exist.
   Where the two disagree, follow the diff and report the difference.
4. **Pick the executor** from the `demo` overlay.
   The overlay says how to launch the app, how to seed baseline state, the base url and the test credentials.
   If the overlay declares a project skill that launches and drives the app, that skill *is* the executor.

   **No overlay is a gap to fill** (`conventions/gaps.md`).
   Look first, in a few targeted `Read` and `Grep` calls sent in one message, for these facts:

   - **What drives the app**: a Playwright, Cypress, Selenium or Capybara config, an e2e suite and its command, a browser MCP server this session has, or a project skill that drives the app.
   - **How to launch it**: the dev-server recipe in the `justfile`, `Makefile`, `package.json` scripts or `Procfile`, and its port.
   - **How to create what a script needs**, when no existing record fits: the test suite's console, runner or factory, the app's own forms, or a seed task.
   - **How to authenticate**: the test user, the dev-login route, or the header or marker the project's CLAUDE.md documents.

   Then propose them in **one** `AskUserQuestion`, with a question per fact.
   Put your finding first in each question, or the conservative choice where you found nothing.
   Add one more question: **where the report should go** (point 10).
   Show the resulting overlay, write it to `.f10/instructions/demo.md`, and continue the run.
   Later runs read the overlay and never ask again.
   If the user declines, the run **degrades to hands-on** and names the missing fact, which is a normal end.
5. **Set the state up, out loud.**
   Reuse a record that already fits.
   Otherwise, say what you will create, then **create** only the missing records through the overlay's mechanism, in the database the overlay names.
   Created records stay.
   The report names each one with its id, and removing them is the user's call.
6. **Execute, or hand over.**
   Static: run the script, capture evidence as you go, and note anything that behaved unlike the script.
   Hands-on: seed the state, leave the app running, and print the script with its urls filled in.
7. **Show before and after** only when the change modifies something visible that **already existed**.
   Run the same script on the same seeded state on both sides, and name each side's commit.
   Otherwise skip it and say why in one line.
   For example, a new surface has no before except an empty page or a 404, and a migration cannot hold the same state on both sides.
8. **Capture one artifact per user-visible claim.**
   A button that grew and changed colour is one claim, so it gets one shot.
   A change across five screens earns more shots.
   A hands-on scenario is the shortest path that touches every claim, and takes a few minutes at most.
9. **Claim only what you captured.**
   Every sentence about behaviour points at an artifact this run produced.
   List what could not be demonstrated, and why.
   The only statement derived from the diff alone is point 2's, and it is phrased as what the change *does*.
10. **Report, in two places.**
    In chat, report per `conventions/report.md`: the task, the PR url, and **only the artifacts this run wrote**.
    Then write prose in this order:
    1. what changed
    2. what was demonstrated
    3. what was not demonstrated, and why
    4. what the run created and left behind
    5. the script

    When the run wrote a report file, close with `f10 demo open <TASK-ID>`.

    On disk, write `report.html` in the evidence directory, in **both** modes.
    It opens with what changed.
    A static report puts each shot under its claim, with before and after side by side.
    A hands-on report holds the script as a checklist.
    The page is self-contained: inline styles, images by relative path, legible in light and dark, with no build step and no server.

    **Where the page goes is the project's choice**, made in point 4 and recorded in the overlay: a local file only, a local file that is also published, or published only.
    `--publish` and `--local` override that choice for one run.
    Publishing makes a **private page on the user's own account**, but the page still leaves the machine.
    So publish only what the overlay declares, or what the user asked for in as many words.
    A published page carries **no f10 traces**, whatever the project's visibility (`conventions/context.md`).
    Upload the screenshots with the page, and use the urls the host returns.
    Nothing goes to the PR or the tracker.

**On failure:** if the app will not start, or the needed state cannot be reached, stop and report, and say whether setup created anything.
A script that ran while the feature failed to appear is **not** a failure.
Report the feature as not demonstrated, and leave what to do about it to the user.
