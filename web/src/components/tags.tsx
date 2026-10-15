import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ListFilter, Plus, X } from "lucide-react";
import { useState, type KeyboardEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { HoverCard, Popover } from "radix-ui";
import { Section } from "@/components/common";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { autoTagColor, normalizeTag, tagChipClass, tagColors, tagDotClass } from "@/lib/tags";
import { cn } from "@/lib/utils";
import { api, type TagInfo } from "../api";

const maxTags = 10;

/** The tags in use, and the color of any tag. */
export function useTags() {
  const tags = useQuery({ queryKey: ["tags"], queryFn: api.tags, staleTime: 60_000 });
  const list = tags.data?.tags ?? [];
  const colorOf = (name: string) => list.find((t) => t.name === name)?.color ?? autoTagColor(name);
  return { list, colorOf };
}

export function TagChip({ name, color, className, children }: { name: string; color: string; className?: string; children?: ReactNode }) {
  return (
    <span className={cn("inline-flex max-w-full items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium ring-1 ring-inset", tagChipClass(color), className)}>
      <span className="truncate">{name}</span>
      {children}
    </span>
  );
}

/** A card's tags, read-only. With max, one line: the rest folds into a +N chip that shows them on hover. */
export function TagChips({ tags, max, className }: { tags?: string[]; max?: number; className?: string }) {
  const { colorOf } = useTags();
  if (!tags?.length) return null;
  const rest = max === undefined ? [] : tags.slice(max);
  return (
    <div className={cn("flex gap-1", max === undefined ? "flex-wrap" : "flex-nowrap", className)}>
      {tags.slice(0, max).map((t) => (
        <TagChip key={t} name={t} color={colorOf(t)} />
      ))}
      {rest.length > 0 && (
        <HoverCard.Root openDelay={200} closeDelay={100}>
          <HoverCard.Trigger asChild>
            <span aria-label={`Also ${rest.join(", ")}`} className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground">
              +{rest.length}
            </span>
          </HoverCard.Trigger>
          <HoverCard.Portal>
            <HoverCard.Content
              sideOffset={4}
              className="z-50 flex max-w-xs flex-wrap gap-1 rounded-md border bg-popover p-2 shadow-md"
              onClick={(e) => e.stopPropagation()}
            >
              {rest.map((t) => (
                <TagChip key={t} name={t} color={colorOf(t)} />
              ))}
            </HoverCard.Content>
          </HoverCard.Portal>
        </HoverCard.Root>
      )}
    </div>
  );
}

/** The tags filter of a list, kept in the URL (?tag=, repeated). */
export function useTagFilter() {
  const [params, setParams] = useSearchParams();
  const selected = params.getAll("tag");
  const set = (tags: string[]) =>
    setParams(
      (p) => {
        p.delete("tag");
        for (const t of tags) p.append("tag", t);
        return p;
      },
      { replace: true },
    );
  /** Whether an item carries every selected tag. */
  const matches = (tags?: string[]) => selected.every((t) => tags?.includes(t));
  return { selected, set, matches };
}

/**
 * Narrows a list to the items carrying every picked tag: a menu of the tags
 * its items carry, with how many do; its trigger counts the picked ones.
 */
export function TagFilter({ items, filter }: { items: (string[] | undefined)[]; filter: ReturnType<typeof useTagFilter> }) {
  const { colorOf } = useTags();
  const counts = new Map<string, number>();
  for (const t of items.flat()) if (t) counts.set(t, (counts.get(t) ?? 0) + 1);
  // A tag picked from a link stays listed, to be cleared.
  const names = [...new Set([...counts.keys(), ...filter.selected])].sort();
  if (names.length === 0) return null;
  const toggle = (t: string) => filter.set(filter.selected.includes(t) ? filter.selected.filter((s) => s !== t) : [...filter.selected, t]);
  return (
    <div className="flex flex-wrap items-center justify-end gap-1.5">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline">
            <ListFilter data-icon="inline-start" />
            Tags
            {filter.selected.length > 0 && <span className="rounded-full bg-primary px-1.5 text-xs text-primary-foreground tabular-nums">{filter.selected.length}</span>}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="max-h-80 w-56">
          <DropdownMenuLabel>Show what carries every tag</DropdownMenuLabel>
          {names.map((t) => (
            <DropdownMenuCheckboxItem key={t} checked={filter.selected.includes(t)} onSelect={(e) => e.preventDefault()} onCheckedChange={() => toggle(t)}>
              <span className={cn("size-2 shrink-0 rounded-full", tagDotClass(colorOf(t)))} />
              <span className="flex-1 truncate">{t}</span>
              <span className="text-xs text-muted-foreground tabular-nums">{counts.get(t) ?? 0}</span>
            </DropdownMenuCheckboxItem>
          ))}
          {filter.selected.length > 0 && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => filter.set([])}>
                <X />
                Clear
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/**
 * Picks a tag to add: existing tags first, the most used on top and those
 * starting with the text before those containing it, then creating a new one.
 */
function TagInput({ exclude, pending, onAdd }: { exclude: string[]; pending: boolean; onAdd: (tag: string) => void }) {
  const { list } = useTags();
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const query = normalizeTag(text);
  const used = (t: TagInfo) => t.projects + t.services;
  const matches = list
    .filter((t) => !exclude.includes(t.name) && t.name.includes(query))
    .sort((a, b) => Number(b.name.startsWith(query)) - Number(a.name.startsWith(query)) || used(b) - used(a) || a.name.localeCompare(b.name))
    .slice(0, 8);
  const creates = !!query && !exclude.includes(query) && !list.some((t) => t.name === query);
  const options = [...matches.map((t) => t.name), ...(creates ? [query] : [])];
  const pick = (tag: string) => {
    onAdd(tag);
    setText("");
    setActive(0);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
      const n = options.length || 1;
      setActive((i) => (i + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (open && options[active]) pick(options[active]);
    } else if (e.key === "Escape" && open) {
      e.stopPropagation();
      setOpen(false);
    }
  };

  return (
    <Popover.Root open={open && options.length > 0}>
      <Popover.Anchor className="relative max-w-sm">
        <Plus className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <input
          role="combobox"
          aria-label="Add a tag"
          aria-expanded={open && options.length > 0}
          aria-controls="tag-options"
          aria-activedescendant={open && options[active] ? `tag-option-${active}` : undefined}
          aria-autocomplete="list"
          placeholder="Add a tag"
          maxLength={32}
          value={text}
          aria-busy={pending}
          onChange={(e) => {
            setText(e.target.value);
            setActive(0);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onBlur={() => setOpen(false)}
          onKeyDown={onKeyDown}
          className="h-8 w-full rounded-md border bg-background pr-2.5 pl-7 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
        />
      </Popover.Anchor>
      <Popover.Portal>
        {/* The input keeps the focus: the list only follows it. */}
        <Popover.Content
          align="start"
          sideOffset={4}
          onOpenAutoFocus={(e) => e.preventDefault()}
          className="z-50 max-h-72 w-(--radix-popper-anchor-width) overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
        >
          <ul id="tag-options" role="listbox" aria-label="Tags">
            {options.map((name, i) => {
              const info = list.find((t) => t.name === name);
              return (
                <li
                  key={name}
                  id={`tag-option-${i}`}
                  role="option"
                  aria-selected={i === active}
                  // Keeps the input focused, so the list stays open for the next tag.
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => pick(name)}
                  className={cn("flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm", i === active && "bg-accent text-accent-foreground")}
                >
                  {info ? (
                    <>
                      <span className={cn("size-2 shrink-0 rounded-full", tagDotClass(info.color))} />
                      <span className="flex-1 truncate">{name}</span>
                      <span className="text-xs text-muted-foreground">{usage(info)}</span>
                    </>
                  ) : (
                    <>
                      <Plus className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="flex-1 truncate">
                        Create <span className="font-medium">{name}</span>
                      </span>
                    </>
                  )}
                </li>
              );
            })}
          </ul>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

const usage = (t: TagInfo) =>
  [t.projects && `${t.projects} project${t.projects === 1 ? "" : "s"}`, t.services && `${t.services} service${t.services === 1 ? "" : "s"}`]
    .filter(Boolean)
    .join(", ");

/**
 * Edits the tags of a project or a service: each change saves at once. A
 * tag's menu also sets its color, shared by everything carrying it.
 */
export function TagsCard({
  tags,
  description,
  disabled,
  pending,
  onSave,
}: {
  tags: string[];
  description: ReactNode;
  disabled?: boolean;
  pending: boolean;
  onSave: (tags: string[]) => void;
}) {
  const qc = useQueryClient();
  const { list, colorOf } = useTags();
  const color = useMutation({
    meta: { error: "Couldn't change the tag's color" },
    mutationFn: ({ name, color }: { name: string; color: string }) => api.setTagColor(name, color),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tags"] }),
  });
  const custom = (name: string) => list.find((t) => t.name === name)?.custom ?? false;

  return (
    <Section title="Tags" description={description}>
      <div className="space-y-3">
        {tags.length > 0 && (
          <ul aria-label="Tags" className="flex flex-wrap gap-1.5">
            {tags.map((t) => (
              <li key={t}>
                <DropdownMenu>
                  <DropdownMenuTrigger
                    disabled={disabled}
                    aria-label={`Tag ${t}: color or remove`}
                    className={cn(
                      "inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset outline-none focus-visible:ring-2 focus-visible:ring-ring enabled:hover:brightness-95 disabled:cursor-default",
                      tagChipClass(colorOf(t)),
                    )}
                  >
                    <span className={cn("size-2 rounded-full", tagDotClass(colorOf(t)))} />
                    {t}
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="start" className="w-52">
                    <DropdownMenuLabel>Color, for everything tagged {t}</DropdownMenuLabel>
                    <div role="group" aria-label="Color" className="grid grid-cols-6 gap-1 px-2 pb-2">
                      {tagColors.map((c) => (
                        <DropdownMenuItem
                          key={c}
                          aria-label={c}
                          title={c}
                          onSelect={() => color.mutate({ name: t, color: c })}
                          className={cn("flex size-7 items-center justify-center p-0")}
                        >
                          <span className={cn("flex size-4 items-center justify-center rounded-full text-white", tagDotClass(c))}>
                            {colorOf(t) === c && <Check className="size-3" />}
                          </span>
                        </DropdownMenuItem>
                      ))}
                    </div>
                    {custom(t) && (
                      <DropdownMenuItem onSelect={() => color.mutate({ name: t, color: "" })}>
                        <span className={cn("size-3 rounded-full", tagDotClass(autoTagColor(t)))} />
                        Back to its own color
                      </DropdownMenuItem>
                    )}
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onSelect={() => onSave(tags.filter((x) => x !== t))}>
                      <X />
                      Remove
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </li>
            ))}
          </ul>
        )}
        {!disabled && tags.length < maxTags && <TagInput exclude={tags} pending={pending} onAdd={(t) => onSave([...tags, t])} />}
      </div>
    </Section>
  );
}
