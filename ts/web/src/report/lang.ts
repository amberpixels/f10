import { go } from "@codemirror/lang-go";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { yaml } from "@codemirror/lang-yaml";
import { StreamLanguage } from "@codemirror/language";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import type { Extension } from "@codemirror/state";

/** language is the highlighting for a path; an unknown kind of file is plain text. */
export function language(path: string): Extension[] {
  const name = path.split("/").pop()!.toLowerCase();
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1) : "";
  switch (ext) {
    case "go":
      return [go()];
    case "md":
      return [markdown()];
    case "json":
    case "jsonl":
      return [json()];
    case "js":
    case "mjs":
    case "cjs":
    case "jsx":
      return [javascript({ jsx: ext === "jsx" })];
    case "ts":
    case "tsx":
      return [javascript({ typescript: true, jsx: ext === "tsx" })];
    case "yml":
    case "yaml":
      return [yaml()];
    case "sh":
    case "bash":
    case "zsh":
      return [StreamLanguage.define(shell)];
  }
  return name === "justfile" || name === "makefile" ? [StreamLanguage.define(shell)] : [];
}
