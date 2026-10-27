export type Box = { id: string; x: number; y: number; w: number; h: number };

/**
 * Moves boxes so none overlap, `gap` apart at least. Boxes are placed in
 * order: each keeps its place if free, else takes the nearest free spot next
 * to the ones placed before it (right and down cost less than left and up,
 * so a layout spreads the way it reads). Returns the IDs that moved.
 */
export function separate(boxes: Box[], gap: number): string[] {
  const placed: Box[] = [];
  const moved: string[] = [];
  const hits = (b: Box, x: number, y: number) => placed.some((p) => x < p.x + p.w + gap && p.x < x + b.w + gap && y < p.y + p.h + gap && p.y < y + b.h + gap);
  for (const b of boxes) {
    if (hits(b, b.x, b.y)) {
      // Candidates: against each placed box's sides, on either axis. The one
      // right of them all is always free.
      const xs = [b.x, ...placed.flatMap((p) => [p.x + p.w + gap, p.x - gap - b.w])];
      const ys = [b.y, ...placed.flatMap((p) => [p.y + p.h + gap, p.y - gap - b.h])];
      const cost = (x: number, y: number) => (x < b.x ? 1.5 : 1) * Math.abs(x - b.x) + (y < b.y ? 1.5 : 1) * Math.abs(y - b.y);
      const spots = xs.flatMap((x) => ys.map((y) => ({ x, y, c: cost(x, y) }))).sort((s, t) => s.c - t.c);
      // shortcut: O(n³) per box in the worst case, fine for the tens of nodes a frame holds; index boxes if maps reach hundreds.
      const spot = spots.find((s) => !hits(b, s.x, s.y))!;
      b.x = spot.x;
      b.y = spot.y;
      moved.push(b.id);
    }
    placed.push(b);
  }
  return moved;
}
