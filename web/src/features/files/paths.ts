export const join = (dir: string, name: string) => (dir ? `${dir}/${name}` : name);
/** "@container" is the first running replica's filesystem, "@<container ID>" a given one's. */
export const CONTAINER = "@container";
export const inContainer = (p: string) => p.startsWith("@");

/** A name for an entry in a folder: no slashes, not . or .. */
export const nameProblem = (name: string) =>
  name.includes("/") ? "A name can't contain /." : name === "." || name === ".." ? "Pick another name." : null;

/** A destination "<volume>/<path>": relative, without . or .. segments. */
export const pathProblem = (p: string) =>
  p.startsWith("/") ? "Start with the volume's name, e.g. data/mods." : p.split("/").some((s) => s === "." || s === "..") ? "Use a path without . or .." : null;
