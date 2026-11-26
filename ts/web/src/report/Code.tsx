import { syntaxHighlighting } from "@codemirror/language";
import { unifiedMergeView } from "@codemirror/merge";
import { EditorState } from "@codemirror/state";
import { EditorView, lineNumbers } from "@codemirror/view";
import { classHighlighter } from "@lezer/highlight";
import { useEffect, useRef } from "preact/hooks";

import { language } from "./lang";

interface Props {
  path: string;
  doc: string;
  /** With it, the view is a diff from original to doc. */
  original?: string;
}

/**
 * Code is a read-only editor. Highlighting goes through class names, so the
 * stylesheet's light and dark tokens color it and no theme is swapped.
 */
export function Code({ path, doc, original }: Props) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const extensions = [
      EditorState.readOnly.of(true),
      EditorView.editable.of(false),
      lineNumbers(),
      syntaxHighlighting(classHighlighter),
      ...language(path),
    ];
    if (original !== undefined) {
      extensions.push(
        unifiedMergeView({ original, mergeControls: false, collapseUnchanged: { margin: 3, minSize: 8 } }),
      );
    }
    const view = new EditorView({ parent: ref.current!, state: EditorState.create({ doc, extensions }) });
    return () => view.destroy();
  }, [path, doc, original]);

  return <div class="code" ref={ref} />;
}
