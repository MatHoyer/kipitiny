import { Box, type LucideIcon } from "lucide-react";
import type { ComponentType, SVGProps } from "react";
import type { ServiceKind } from "@/api";
import { PostgresIcon, RedisIcon } from "@/components/brand-icons";
import { cn } from "@/lib/utils";

type Logo = { label: string; color: string; Icon: ComponentType<SVGProps<SVGSVGElement>> };

/** Known service logos by icon name; templates add theirs here. */
export const serviceLogos: Record<string, Logo> = {
  postgres: { label: "PostgreSQL", color: "#4169E1", Icon: PostgresIcon },
  redis: { label: "Redis", color: "#FF4438", Icon: RedisIcon },
};

/** Other names images go by, e.g. bitnami/postgresql. */
const aliases: Record<string, string> = { postgresql: "postgres" };

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
export function serviceLogo(s: IconSource): Logo | undefined {
  for (const name of [s.icon, s.kind !== "app" ? s.kind : undefined, s.image && imageBase(s.image)]) {
    const logo = name && serviceLogos[aliases[name] ?? name];
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
  const logo = serviceLogo(service);
  // Decorative unless labelled, like the brand icons.
  if (logo) return <logo.Icon className={className} style={{ color: logo.color }} {...props} />;
  const Fallback = fallback;
  return Fallback && <Fallback className={className} />;
}

/** The service's logo on a tinted tile, like IconTile. */
export function ServiceIconTile({ service, className }: { service: IconSource; className?: string }) {
  const logo = serviceLogo(service);
  return (
    <span
      className={cn("flex size-10 shrink-0 items-center justify-center rounded-lg", !logo && "bg-primary/10 text-primary", className)}
      style={logo && { backgroundColor: `${logo.color}1a` }}
    >
      <ServiceIcon service={service} aria-label={logo?.label} className="size-5" />
    </span>
  );
}
