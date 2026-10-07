import { ArrowRight, Bot, Download, FileCode, type LucideIcon } from "lucide-react";
import { Link } from "react-router";
import { CopyButton } from "@/components/common";
import { Button } from "@/components/ui/button";
import { SettingsPage } from "./page";

export type DocEntry = {
  slug: string;
  title: string;
  description: string;
  icon: LucideIcon;
  /** What it covers, shown as chips. */
  topics: string[];
  /** Where the manager serves its markdown. */
  source: string;
};

/** The documents the manager ships; a versioned docs site replaces them later (#117). */
export const docs: DocEntry[] = [
  {
    slug: "compose",
    title: "Compose reference",
    description:
      "Describe a project as a docker-compose file: every key kipitiny reads, the x-kipitiny settings, variables and secrets, and how git sync applies it.",
    icon: FileCode,
    topics: ["x-kipitiny", "Databases", "Secrets & .env", "GitOps", "Moving a project"],
    source: "/docs/compose/llms.txt",
  },
];

/** Cards for the documentation, to read here or hand to an LLM. */
export function Docs() {
  return (
    <SettingsPage>
      <p className="max-w-2xl text-sm text-muted-foreground">
        Documentation for this kipitiny version. Read it here, or give the markdown link to an LLM or an agent: it is served without sign-in
        and holds no secrets.
      </p>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {docs.map((d) => (
          <DocCard key={d.slug} doc={d} />
        ))}
      </div>
    </SettingsPage>
  );
}

function DocCard({ doc }: { doc: DocEntry }) {
  const Icon = doc.icon;
  const url = `${window.location.origin}${doc.source}`;
  return (
    <article className="group relative flex flex-col overflow-hidden rounded-xl border bg-card transition-all hover:-translate-y-0.5 hover:border-foreground/25 hover:shadow-lg focus-within:ring-3 focus-within:ring-ring/50">
      <div className="relative h-28 overflow-hidden bg-linear-to-br from-primary/20 via-primary/5 to-transparent">
        <Icon aria-hidden className="absolute -right-6 -bottom-8 size-40 text-primary/10 transition-transform duration-500 group-hover:-rotate-6 group-hover:scale-105" />
        <span className="absolute bottom-4 left-5 flex size-12 items-center justify-center rounded-xl bg-background/80 text-primary shadow-sm ring-1 ring-foreground/10 backdrop-blur">
          <Icon className="size-6" />
        </span>
        <span className="absolute top-3 right-3 flex items-center gap-1 rounded-full bg-background/70 px-2 py-0.5 text-[11px] font-medium text-muted-foreground ring-1 ring-foreground/10 backdrop-blur">
          <Bot className="size-3" />
          LLM-ready
        </span>
      </div>
      <div className="flex flex-1 flex-col gap-3 p-5">
        <h2 className="text-base font-semibold tracking-tight">
          {/* The whole card opens the document; the buttons below sit above this link. */}
          <Link to={`/docs/${doc.slug}`} className="outline-none after:absolute after:inset-0">
            {doc.title}
          </Link>
        </h2>
        <p className="text-sm text-muted-foreground">{doc.description}</p>
        <div className="flex flex-wrap gap-1.5">
          {doc.topics.map((t) => (
            <span key={t} className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
              {t}
            </span>
          ))}
        </div>
        <div className="mt-auto flex items-center justify-between gap-2 border-t pt-3">
          <span className="flex items-center gap-1 text-sm font-medium text-primary">
            Read
            <ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" />
          </span>
          <div className="relative z-10 flex items-center gap-1">
            <CopyButton value={url} label="Copy the markdown link (for an LLM)" />
            <Button variant="ghost" size="icon-xs" asChild>
              <a href={doc.source} download={`kipitiny-${doc.slug}.md`} aria-label="Download markdown" title="Download markdown">
                <Download />
              </a>
            </Button>
          </div>
        </div>
      </div>
    </article>
  );
}
