import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { html } from "@codemirror/lang-html";
import { bracketMatching, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { useEffect, useRef } from "react";

/** Mid-lightness hues that read on both themes, like the SQL console's. */
const highlight = HighlightStyle.define([
  { tag: [t.tagName, t.angleBracket], color: "oklch(0.58 0.15 250)" },
  { tag: t.attributeName, color: "oklch(0.6 0.13 60)" },
  { tag: [t.attributeValue, t.string], color: "oklch(0.58 0.13 150)" },
  { tag: [t.keyword, t.definitionKeyword, t.modifier], color: "oklch(0.58 0.15 320)", fontWeight: "500" },
  { tag: [t.propertyName, t.variableName], color: "oklch(0.6 0.12 200)" },
  { tag: [t.number, t.bool, t.null, t.color, t.unit], color: "oklch(0.62 0.15 40)" },
  { tag: [t.comment, t.lineComment, t.blockComment], color: "var(--muted-foreground)", fontStyle: "italic" },
  { tag: [t.punctuation, t.operator, t.separator], color: "var(--muted-foreground)" },
  { tag: [t.documentMeta, t.processingInstruction], color: "var(--muted-foreground)" },
]);

const theme = EditorView.theme({
  "&": { fontSize: "0.8rem", backgroundColor: "transparent", maxHeight: "32rem" },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.55" },
  ".cm-content": { padding: "0.5rem 0", caretColor: "var(--foreground)", minHeight: "16rem" },
  ".cm-gutters": { backgroundColor: "transparent", borderRight: "1px solid var(--border)", color: "var(--muted-foreground)" },
  ".cm-activeLine": { backgroundColor: "color-mix(in oklab, var(--muted) 60%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--foreground)" },
  ".cm-cursor": { borderLeftColor: "var(--foreground)", borderLeftWidth: "2px" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in oklab, var(--primary) 18%, transparent) !important",
  },
  ".cm-matchingBracket": { backgroundColor: "color-mix(in oklab, var(--primary) 15%, transparent)", outline: "none" },
});

/** An HTML editor with syntax colors (CSS and scripts inside too), line numbers, undo and tab indent. */
export default function HtmlEditor({ value, onChange, label }: { value: string; onChange: (v: string) => void; label: string }) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const latest = useRef(onChange);
  latest.current = onChange;

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          history(),
          closeBrackets(),
          bracketMatching(),
          html(),
          syntaxHighlighting(highlight),
          keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, indentWithTab]),
          theme,
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ "aria-label": label }),
          EditorView.updateListener.of((u) => u.docChanged && latest.current(u.state.doc.toString())),
        ],
      }),
    });
    view.current = v;
    return () => v.destroy();
    // Created once; outside changes (reset, prefill) sync below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);

  return <div ref={host} className="overflow-hidden rounded-md border bg-transparent focus-within:ring-[3px] focus-within:ring-ring/50" />;
}
