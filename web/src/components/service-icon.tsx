import { useQuery } from "@tanstack/react-query";
import { Box, type LucideIcon } from "lucide-react";
import type { ComponentType, SVGProps } from "react";
import { api, type ServiceKind, type TemplateLogo } from "@/api";
import { NginxIcon, NodeIcon, PostgresIcon, RedisIcon } from "@/components/brand-icons";
import { cn } from "@/lib/utils";

type Logo = { label: string; color: string; Icon: ComponentType<SVGProps<SVGSVGElement>> };

/** Logos drawn here, by icon name; templates bring the others (useServiceLogos). */
export const serviceLogos: Record<string, Logo> = {
  postgres: { label: "PostgreSQL", color: "#4169E1", Icon: PostgresIcon },
  redis: { label: "Redis", color: "#FF4438", Icon: RedisIcon },
  nginx: { label: "NGINX", color: "#009639", Icon: NginxIcon },
  node: { label: "Node.js", color: "#5FA04E", Icon: NodeIcon },
};

/** Other names images go by, e.g. bitnami/postgresql. */
const builtinAliases: Record<string, string> = {
  postgresql: "postgres",
  "nginx-unprivileged": "nginx",
  nodejs: "node",
};

export type Logos = { logos: Record<string, Logo>; aliases: Record<string, string> };

const builtins: Logos = { logos: serviceLogos, aliases: builtinAliases };

/** A template's logo, drawn as an image: an SVG in <img> never runs scripts. */
function imageLogo(l: TemplateLogo): Logo {
  const src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(l.svg)}`;
  const Icon = ({ className, "aria-label": label }: SVGProps<SVGSVGElement>) => (
    <img src={src} alt={label ?? ""} className={cn("object-contain", className)} draggable={false} />
  );
  return { label: l.label, color: l.color, Icon };
}

function merge(fetched: TemplateLogo[]): Logos {
  const logos = { ...serviceLogos };
  const aliases = { ...builtinAliases };
  for (const l of fetched) {
    logos[l.name] ??= imageLogo(l);
    for (const image of l.images) if (image !== l.name) aliases[image] ??= l.name;
  }
  return { logos, aliases };
}

/** Every known logo: the built-in ones and those the templates bring. */
export function useServiceLogos(): Logos {
  const q = useQuery({ queryKey: ["template-logos"], queryFn: api.templateLogos, staleTime: Infinity, select: merge });
  return q.data ?? builtins;
}

/** What picks a service's logo; backups carry icon and kind only. */
export type IconSource = { icon?: string; kind?: ServiceKind; image?: string };

/** The image's base name (ghcr.io/n8n-io/n8n:1 → n8n), as core.IconHint. */
function imageBase(image: string) {
  const name = image.split("@")[0];
  const colon = name.lastIndexOf(":");
  const repo = colon > name.lastIndexOf("/") ? name.slice(0, colon) : name;
  return repo.slice(repo.lastIndexOf("/") + 1).toLowerCase();
}

/** The logo for a service: its icon, else its kind's, else its image's. */
export function serviceLogo(s: IconSource, { logos, aliases }: Logos = builtins): Logo | undefined {
  for (const name of [s.icon, s.kind !== "app" ? s.kind : undefined, s.image && imageBase(s.image)]) {
    const logo = name && logos[aliases[name] ?? name];
    if (logo) return logo;
  }
  return undefined;
}

/** A service's logo in its brand color, or fallback (none with null). */
export function ServiceIcon({
  service,
  fallback = Box,
  className,
  ...props
}: { service: IconSource; fallback?: LucideIcon | null } & SVGProps<SVGSVGElement>) {
  const logo = serviceLogo(service, useServiceLogos());
  // Decorative unless labelled, like the brand icons.
  if (logo) return <logo.Icon className={className} style={{ color: logo.color }} {...props} />;
  const Fallback = fallback;
  return Fallback && <Fallback className={className} />;
}

/** The service's logo on a tinted tile, like IconTile. */
export function ServiceIconTile({ service, className }: { service: IconSource; className?: string }) {
  const logo = serviceLogo(service, useServiceLogos());
  return (
    <span
      className={cn("flex size-10 shrink-0 items-center justify-center rounded-lg", !logo && "bg-primary/10 text-primary", className)}
      style={logo && { backgroundColor: `${logo.color}1a` }}
    >
      <ServiceIcon service={service} aria-label={logo?.label} className="size-5" />
    </span>
  );
}
