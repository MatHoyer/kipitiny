import { autocompletion, closeBrackets, closeBracketsKeymap, closeCompletion, completionKeymap } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { MariaSQL, MySQL, PostgreSQL, sql, type SQLDialect, type SQLNamespace } from "@codemirror/lang-sql";
import { bracketMatching, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, keymap, placeholder as placeholderExt } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { useEffect, useRef } from "react";

/** Colors come from the app's tokens and the database hue, so both themes follow. */
const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: "var(--db)", fontWeight: "500" },
  { tag: [t.string, t.special(t.string)], color: "oklch(0.55 0.13 150)" },
  { tag: [t.number, t.bool, t.null], color: "oklch(0.62 0.15 60)" },
  { tag: [t.lineComment, t.blockComment], color: "var(--muted-foreground)", fontStyle: "italic" },
  { tag: [t.typeName, t.standard(t.name)], color: "oklch(0.55 0.12 300)" },
  { tag: [t.punctuation, t.operator], color: "var(--muted-foreground)" },
]);

const theme = EditorView.theme({
  "&": { fontSize: "0.85rem", backgroundColor: "transparent" },
  "&.cm-focused": { outline: "none" },
  ".cm-content": { fontFamily: "var(--font-mono)", padding: "0.75rem 0", caretColor: "var(--db)", minHeight: "7.5rem" },
  ".cm-line": { padding: "0 1rem" },
  ".cm-cursor": { borderLeftColor: "var(--db)", borderLeftWidth: "2px" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in oklab, var(--db) 18%, transparent) !important",
  },
  ".cm-placeholder": { color: "var(--muted-foreground)" },
  ".cm-matchingBracket": { backgroundColor: "color-mix(in oklab, var(--db) 15%, transparent)", outline: "none" },
  ".cm-tooltip": { border: "1px solid var(--border)", backgroundColor: "var(--popover)", borderRadius: "0.5rem", overflow: "hidden" },
  ".cm-tooltip-autocomplete > ul": { fontFamily: "var(--font-mono)", fontSize: "0.8rem", maxHeight: "14rem" },
  ".cm-tooltip-autocomplete > ul > li": { padding: "0.2rem 0.6rem !important" },
  ".cm-tooltip-autocomplete > ul > li[aria-selected]": { backgroundColor: "color-mix(in oklab, var(--db) 14%, transparent)", color: "inherit" },
  ".cm-completionDetail": { color: "var(--muted-foreground)", fontStyle: "normal", marginLeft: "0.75rem" },
});

const dialects: Record<SqlDialect, SQLDialect> = { postgres: PostgreSQL, mysql: MySQL, mariadb: MariaSQL };
export type SqlDialect = "postgres" | "mysql" | "mariadb";

/**
 * A SQL editor with the database's highlighting and completion of its own
 * tables and columns. Mod-Enter runs.
 */
export default function SqlEditor({
  value,
  onChange,
  onRun,
  schema,
  dialect,
  defaultSchema,
  placeholder,
  label,
}: {
  value: string;
  onChange: (v: string) => void;
  onRun: () => void;
  schema: SQLNamespace;
  dialect: SqlDialect;
  /** Where unqualified names are looked up: public, or MySQL's current database. */
  defaultSchema: string;
  placeholder: string;
  label: string;
}) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const lang = useRef(new Compartment());
  // The callbacks change every render; the editor reads the latest.
  const latest = useRef({ onChange, onRun });
  latest.current = { onChange, onRun };

  useEffect(() => {
    const v = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          Prec.highest(keymap.of([{ key: "Mod-Enter", run: (v) => (closeCompletion(v), latest.current.onRun(), true) }])),
          history(),
          closeBrackets(),
          bracketMatching(),
          autocompletion({ icons: false }),
          keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...completionKeymap, indentWithTab]),
          lang.current.of(sql({ dialect: dialects[dialect], schema, defaultSchema, upperCaseKeywords: true })),
          syntaxHighlighting(highlight),
          theme,
          EditorView.lineWrapping,
          placeholderExt(placeholder),
          EditorView.contentAttributes.of({ "aria-label": label }),
          EditorView.updateListener.of((u) => u.docChanged && latest.current.onChange(u.state.doc.toString())),
        ],
      }),
    });
    view.current = v;
    v.focus();
    return () => v.destroy();
    // Created once; value and schema sync below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    view.current?.dispatch({ effects: lang.current.reconfigure(sql({ dialect: dialects[dialect], schema, defaultSchema, upperCaseKeywords: true })) });
  }, [schema, dialect, defaultSchema]);

  // Outside changes (a query picked from history) replace the text.
  useEffect(() => {
    const v = view.current;
    if (v && v.state.doc.toString() !== value) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);

  return <div ref={host} />;
}
