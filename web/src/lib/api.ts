export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.headers.get("Content-Type")?.includes("json") ? res.json() : (res.text() as T);
}

/**
 * Runs a call that streams its steps as NDJSON (`?progress`): onStep gets each step as it starts, the promise
 * resolves to the result, or rejects with the error the stream ends with.
 */
export async function progress<T>(path: string, init: RequestInit, onStep: (step: string) => void): Promise<T> {
  const res = await fetch(`/api${path}${path.includes("?") ? "&" : "?"}progress`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init.headers },
  });
  if (!res.ok || !res.body) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error ?? res.statusText);
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += value;
    const lines = buf.split("\n");
    buf = lines.pop()!;
    for (const line of lines) {
      if (!line) continue;
      const msg = JSON.parse(line) as { step?: string; result?: T; error?: string };
      if (msg.step) onStep(msg.step);
      else if (msg.error !== undefined) throw new ApiError(400, msg.error);
      else return msg.result as T;
    }
  }
  throw new ApiError(0, "the connection closed before it finished");
}

/** Checks a saved integration still works; with onStep, reports each step as it starts. */
export const test = (path: string, onStep?: (step: string) => void) =>
  onStep ? progress<void>(path, { method: "POST" }, onStep) : request<void>(path, { method: "POST" });

export const json = (method: string, body: unknown): RequestInit => ({ method, body: JSON.stringify(body) });
