import { useEffect } from "react";
import { useParams } from "react-router";

/** The documentation lives on the website (site/ in the repo), not in the manager. */
export const DOCS_URL = "https://kipitiny.mathieuhoyer.fr/docs";

/** Old /docs/<page> links of the manager: send them to the same page on the site. */
export function DocsRedirect() {
  const { "*": page = "" } = useParams();
  useEffect(() => {
    window.location.replace(page ? `${DOCS_URL}/${page}` : DOCS_URL);
  }, [page]);
  return null;
}
