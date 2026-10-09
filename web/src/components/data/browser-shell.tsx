import { PanelLeft, Search, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** The browsers' frame: a hideable list on the left, a toolbar, the content, a footer. */
export function BrowserShell({
  sidebar,
  sidebarOpen,
  toolbar,
  footer,
  children,
}: {
  sidebar: ReactNode;
  sidebarOpen: boolean;
  toolbar: ReactNode;
  footer?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex h-[72vh] min-h-96 overflow-hidden rounded-lg border bg-card">
      {sidebarOpen && <aside className="hidden w-56 shrink-0 flex-col border-r md:flex">{sidebar}</aside>}
      <section className="flex min-w-0 flex-1 flex-col">
        <div className="flex min-h-12 flex-wrap items-center gap-1.5 border-b px-2 py-1.5">{toolbar}</div>
        <div className="min-h-0 flex-1 overflow-auto">{children}</div>
        {footer && <div className="flex flex-wrap items-center gap-2 border-t px-2 py-1.5">{footer}</div>}
      </section>
    </div>
  );
}

export function SidebarToggle({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  return (
    <Button variant="outline" size="icon-sm" className="hidden md:inline-flex" title={open ? "Hide list" : "Show list"} aria-pressed={open} onClick={onToggle}>
      <PanelLeft />
    </Button>
  );
}

/** The list's header: its title, a search revealed on demand, extra actions. */
export function SidebarHeader({ title, search, onSearch, actions }: { title: string; search: string; onSearch: (s: string) => void; actions?: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex h-12 shrink-0 items-center gap-1 border-b px-2">
      {open ? (
        <input
          autoFocus
          aria-label={`Find in ${title.toLowerCase()}`}
          placeholder={`Find ${title.toLowerCase()}`}
          value={search}
          onChange={(e) => onSearch(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && (onSearch(""), setOpen(false))}
          className="h-7 min-w-0 flex-1 rounded-md bg-muted px-2 text-sm outline-none"
        />
      ) : (
        <span className="flex-1 px-1 text-sm font-medium">{title}</span>
      )}
      <Button
        variant="ghost"
        size="icon-xs"
        title={open ? "Close search" : `Find ${title.toLowerCase()}`}
        onClick={() => {
          if (open) onSearch("");
          setOpen(!open);
        }}
      >
        {open ? <X /> : <Search />}
      </Button>
      {actions}
    </div>
  );
}

/** A search box that reports its value after typing pauses. */
export function SearchBox({ value, onChange, placeholder, className }: { value: string; onChange: (v: string) => void; placeholder: string; className?: string }) {
  const [text, setText] = useState(value);
  const latest = useRef(onChange);
  latest.current = onChange;
  useEffect(() => {
    if (text === value) return;
    const t = setTimeout(() => latest.current(text), 350);
    return () => clearTimeout(t);
  }, [text, value]);
  return (
    <div className={cn("relative", className)}>
      <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
      <input
        aria-label={placeholder}
        placeholder={placeholder}
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && onChange(text)}
        className="h-8 w-full rounded-md border bg-background pr-7 pl-8 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
      />
      {text && (
        <button
          type="button"
          aria-label="Clear search"
          onClick={() => {
            setText("");
            onChange("");
          }}
          className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        >
          <X className="size-3.5" />
        </button>
      )}
    </div>
  );
}

/** A list entry in the sidebar, selected like the app's own navigation. */
export function SidebarItem({ selected, onClick, title, children }: { selected: boolean; onClick: () => void; title?: string; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      aria-current={selected}
      className={cn(
        "flex w-full items-center gap-2 rounded-md px-2 py-1 text-left font-mono text-xs text-muted-foreground hover:bg-muted hover:text-foreground",
        selected && "bg-muted text-foreground",
      )}
    >
      {children}
    </button>
  );
}
