/** The full value for a detail view: JSON documents indented, the rest as is. */
export function prettyValue(v: string, cut?: boolean) {
  if (cut || !/^\s*[[{]/.test(v)) return v;
  try {
    return JSON.stringify(JSON.parse(v), null, 2);
  } catch {
    return v;
  }
}
