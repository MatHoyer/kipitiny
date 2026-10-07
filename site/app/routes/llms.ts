import { docSetFor, llmsIndex } from "~/lib/docs.server";
import type { Route } from "./+types/llms";

export const loader = ({ request }: Route.LoaderArgs) =>
  new Response(llmsIndex(docSetFor(request)), { headers: { "Content-Type": "text/plain; charset=utf-8" } });
