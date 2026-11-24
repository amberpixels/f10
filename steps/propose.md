# Step · propose - read a set of tasks against the code, and print how to start them

Input: task ids, or a parent, and any start flags the user gave (`--flow`, `--local`, `--pace`, `--agent`, `--model`, `--parallel`).
Output: the proposal `f10 start <ids> --propose` prints, which is the bundles, the order and the one command to run.
This step starts nothing, and it writes no task, no plan and no tracker change.

The proposal reads the tracker and saved plans by itself.
What it cannot read is the code, so this step reads each task against the code and hands the result over as a **facts sheet** (`docs/cli.md` → Proposal).

1. **Read the tasks**, all the calls in one message (`conventions/latency.md`).
   - A parent: `f10 task children <id> --json`, and its open rows are the tasks.
     A draft row stays out, because the proposal leaves it out.
   - Each task: `f10 task read <id>`, and `f10 plan read <id>` when the task has a saved plan.
2. **Read the code each task names**, with a few targeted `Grep` and `Read` calls, sent together.
   Read only what fills the sheet below, because the proposal needs an estimate, not a design.
3. **Write the sheet**: a JSON object keyed by task id, one row per task.
   - `kind`: `feature`, `bug`, `refactor` or `spike`, settled as the **investigate** step settles it.
   - `areas`: the areas the task touches, in the words `flow_by_area` uses when the project declares it.
   - `files`: the files the change will most likely edit.
     The proposal bundles tasks that share a file, and orders two that share one but cannot bundle, so list only files the change edits, never files it only reads.
   - `lines`: the changed lines you expect, as a whole number.
   - `buildsOn`: the tasks in this set whose code this one needs, when no `After:` line says so.
     It is a list, because a task may need two of them.
   - `risky`: a word such as `migration`, when the change is one that should ship alone.

   Leave out a field you cannot fill, because an empty one reads as a fact.
   Write the sheet with one Bash heredoc to a file that `mktemp` names, since only the next command reads it.
4. **Run** `f10 start <ids> [the start flags] --propose --facts <file>`, with the ids as given (a parent stays a parent).
   Print its output verbatim in a plain fenced block.
   The command line under the levels is the proposal's outcome.
   It spells the order: `A,B` stacks B on A, and `C@A@B` waits for A and B to merge.

**On failure:** the command refuses or a task cannot be read (`conventions/failure.md`).
Print the sheet with the error, so the read is not lost.
