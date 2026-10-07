import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { useParams } from "react-router";
import { api } from "@/api";

/** The documentation lives on the website (site/ in the repo), not in the manager. */
const SITE_URL = "https://kipitiny.mathieuhoyer.fr";

/** The docs of a manager version: its minor release's copy (/docs/0.10), or the latest for a dev build. */
export function docsUrl(version?: string) {
  const minor = version?.match(/^(\d+\.\d+)\.\d+$/)?.[1];
  return minor ? `${SITE_URL}/docs/${minor}` : `${SITE_URL}/docs`;
}

/** The docs of the running manager. */
export function useDocsUrl() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  return docsUrl(status.data?.version);
}

/** Old /docs/<page> links of the manager: send them to the same page on the site. */
export function DocsRedirect() {
  const { "*": page = "" } = useParams();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  const done = status.isSuccess || status.isError;
  const url = docsUrl(status.data?.version);
  useEffect(() => {
    if (done) window.location.replace(page ? `${url}/${page}` : url);
  }, [done, url, page]);
  return null;
}
