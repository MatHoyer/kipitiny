import { ApiError } from "@/api";

/** Turns any thrown error into a sentence a user can act on. */
export function friendlyError(err: unknown): string {
  if (err instanceof TypeError) return "Can't reach the manager. Check that it's running and try again.";
  if (!(err instanceof Error)) return "Something went wrong. Try again.";
  if (err instanceof ApiError) {
    const bare = err.message === "" || err.message.toLowerCase() === statusText(err.status);
    switch (true) {
      case err.status === 401 && (bare || err.message === "unauthorized"):
        return "Your session has expired. Sign in again.";
      case err.status === 403 && (bare || err.message === "forbidden"):
        return "You don't have permission to do this.";
      case err.status === 404:
        return "It no longer exists. Refresh the page.";
      case err.status === 429:
        return "Too many attempts. Wait a minute and try again.";
      case err.status >= 500:
        return "Something went wrong on the server. The manager's log has the details.";
    }
  }
  return sentence(err.message);
}

/** Capitalizes the first letter and ends with a period. */
function sentence(msg: string): string {
  const s = msg.trim();
  if (!s) return "Something went wrong. Try again.";
  const capped = s[0].toUpperCase() + s.slice(1);
  return /[.!?]$/.test(capped) ? capped : `${capped}.`;
}

function statusText(status: number): string {
  return (
    { 400: "bad request", 401: "unauthorized", 403: "forbidden", 404: "not found", 409: "conflict" }[status] ?? ""
  );
}
