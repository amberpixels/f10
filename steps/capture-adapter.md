# Step · capture-adapter - create a task when `f10 task create` cannot

The capture step loads this when `f10 task create` exits 3, because neither a driver nor the host's issues create tasks here.
The task goes through the create adapter `project.md → Tracker` declares: a skill to invoke, or a command.

- **Render the draft for the tracker**, because markdown is not a safe default:
  - GitHub or GitLab: GFM, each paragraph on one line
  - Notion, through an adapter that takes structured content: blocks, not a markdown string
  - Jira Cloud: not markdown
  - unknown: plain paragraphs, with no markup that reads as noise
- **The `After:` line** is what `f10 task ref <id> --md` prints on a GFM tracker, and the bare id on any other.
- **The title**: when `f10 config` shows a `tracker.title` other than `none`, run `f10 task retitle <id>` right after the adapter creates the task.
  The host's issues take `{id}: {title}` by default, so the key need not be declared.
  A failed retitle is reported, and the task is never created again.
- **Properties** are set by the adapter's own means.
- **A split** links each subtask through the tracker's native parent link, or the **parent** adapter the project declares.
  A tracker with no parent link gets a checklist in the parent's body, one item per subtask title, edited in once the subtasks exist.
