import { findDoc, llmsText } from "~/lib/docs.server";
import type { Route } from "./+types/doc-llms";

export function loader({ params }: Route.LoaderArgs) {
  const doc = findDoc(params.slug);
  if (!doc) throw new Response("Not found", { status: 404 });
  return new Response(llmsText(doc), { headers: { "Content-Type": "text/plain; charset=utf-8" } });
}
