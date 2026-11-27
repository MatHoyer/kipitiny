import { ChevronLeft, ChevronRight, ChevronsLeft } from "lucide-react";
import { Button } from "@kipit-labs/kipitidy/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@kipit-labs/kipitidy/select";

/** First, previous, the page (of how many, when known), its size, next. */
export function Pager({
  page,
  pages,
  approx,
  size,
  sizes,
  unit,
  hasMore,
  onPage,
  onSize,
}: {
  page: number;
  pages: number | null;
  /** The page count is an estimate: shown as ~. */
  approx?: boolean;
  size: number;
  sizes: number[];
  /** rows, documents. */
  unit: string;
  hasMore?: boolean;
  onPage: (page: number) => void;
  onSize: (size: number) => void;
}) {
  return (
    <div className="flex h-8 items-stretch overflow-hidden rounded-md border">
      <Button variant="ghost" size="icon-sm" className="h-auto rounded-none" title="First page" disabled={page === 0} onClick={() => onPage(0)}>
        <ChevronsLeft />
      </Button>
      <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Previous page" disabled={page === 0} onClick={() => onPage(page - 1)}>
        <ChevronLeft />
      </Button>
      <span className="flex items-center border-l px-3 text-sm whitespace-nowrap tabular-nums">
        {page + 1}
        {pages && (
          <span className="ml-1 text-muted-foreground">
            of {approx && "~"}
            {Intl.NumberFormat().format(Math.max(pages, page + 1))}
          </span>
        )}
      </span>
      <Select value={String(size)} onValueChange={(v) => onSize(Number(v))}>
        <SelectTrigger size="sm" aria-label={`${unit[0].toUpperCase()}${unit.slice(1)} per page`} className="h-auto! rounded-none border-0 border-l shadow-none">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {sizes.map((n) => (
            <SelectItem key={n} value={String(n)}>
              {n} {unit} per page
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button variant="ghost" size="icon-sm" className="h-auto rounded-none border-l" title="Next page" disabled={!hasMore} onClick={() => onPage(page + 1)}>
        <ChevronRight />
      </Button>
    </div>
  );
}
