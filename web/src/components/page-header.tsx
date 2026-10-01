import { Fragment, type ReactNode } from "react";
import { Link } from "react-router";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Separator } from "@/components/ui/separator";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { cn } from "@/lib/utils";

/** A step in the breadcrumb; the last one is the current page and needs no link. */
export type Crumb = { label: ReactNode; to?: string };

/**
 * The header every page uses: sidebar button, breadcrumb and actions. On
 * phones only the current page's crumb shows.
 */
export function PageHeader({ crumbs, actions }: { crumbs: Crumb[]; actions?: ReactNode }) {
  const current = crumbs[crumbs.length - 1]?.label;
  return (
    <header className="sticky top-0 z-20 shrink-0 border-b bg-background/90 backdrop-blur">
      {/* The breadcrumb stands in for a visible title; screen readers still get a heading. */}
      {typeof current === "string" && <h1 className="sr-only">{current}</h1>}
      <div className="flex min-h-14 flex-wrap items-center gap-2 px-3 py-2 sm:px-6">
        <SidebarTrigger />
        <Separator orientation="vertical" className="mr-1 data-vertical:h-4 data-vertical:self-center" />
        <Breadcrumb className="min-w-0 flex-1">
          <BreadcrumbList className="flex-nowrap">
            {crumbs.map((crumb, i) => {
              const last = i === crumbs.length - 1;
              return (
                <Fragment key={i}>
                  <BreadcrumbItem className={cn(last ? "min-w-0" : "max-sm:hidden")}>
                    {last ? (
                      <BreadcrumbPage className="flex items-center gap-2 truncate">{crumb.label}</BreadcrumbPage>
                    ) : crumb.to ? (
                      <BreadcrumbLink asChild>
                        <Link to={crumb.to}>{crumb.label}</Link>
                      </BreadcrumbLink>
                    ) : (
                      <span>{crumb.label}</span>
                    )}
                  </BreadcrumbItem>
                  {!last && <BreadcrumbSeparator className="max-sm:hidden" />}
                </Fragment>
              );
            })}
          </BreadcrumbList>
        </Breadcrumb>
        {actions && <div className="flex shrink-0 flex-wrap items-center gap-1.5">{actions}</div>}
      </div>
    </header>
  );
}

/** Page body under the header. */
export function PageBody({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("mx-auto w-full max-w-6xl space-y-6 px-4 py-6 sm:px-6 lg:px-8", className)}>{children}</div>;
}
