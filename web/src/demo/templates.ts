import type { AppTemplate, Service, TemplateLogo } from "../api";
import catalog from "./catalog.json";

/** A service an install creates; domainInput names the input its domain comes from. */
type DemoService = Partial<Service> & Pick<Service, "name" | "image" | "kind"> & { domainInput?: string };

/**
 * The manager's templates, generated from internal/templates/files by
 * `go generate ./internal/templates`: what the gallery lists, the logos they
 * bring and the services an install creates.
 */
export const demoTemplates = catalog.templates as (AppTemplate & { services: DemoService[] })[];
export const demoLogos: TemplateLogo[] = catalog.logos;
