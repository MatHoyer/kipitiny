import { llmsIndex } from "~/lib/docs.server";

export const loader = () =>
  new Response(llmsIndex(), { headers: { "Content-Type": "text/plain; charset=utf-8" } });
