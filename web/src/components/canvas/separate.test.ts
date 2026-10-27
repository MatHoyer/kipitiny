import assert from "node:assert/strict";
import { test } from "node:test";
import { separate, type Box } from "./separate.ts";

const box = (id: string, x: number, y: number, w = 100, h = 50): Box => ({ id, x, y, w, h });
const overlap = (a: Box, b: Box, gap: number) => a.x < b.x + b.w + gap && b.x < a.x + a.w + gap && a.y < b.y + b.h + gap && b.y < a.y + a.h + gap;

test("free boxes stay put", () => {
  const boxes = [box("a", 0, 0), box("b", 200, 0)];
  assert.deepEqual(separate(boxes, 10), []);
  assert.deepEqual(boxes.map((b) => [b.x, b.y]), [[0, 0], [200, 0]]);
});

test("a box on another moves to the nearest side, the first one stays", () => {
  const boxes = [box("a", 0, 0), box("b", 20, 10)];
  assert.deepEqual(separate(boxes, 10), ["b"]);
  assert.deepEqual([boxes[1].x, boxes[1].y], [20, 60]);
  assert.deepEqual([boxes[0].x, boxes[0].y], [0, 0]);
});

test("a push cascades: nothing overlaps after", () => {
  const boxes = [box("drag", 0, 0, 300, 200), ...Array.from({ length: 12 }, (_, i) => box(`n${i}`, (i % 4) * 120, Math.floor(i / 4) * 70))];
  separate(boxes, 16);
  for (const a of boxes) for (const b of boxes) if (a !== b) assert.ok(!overlap(a, b, 16), `${a.id} overlaps ${b.id}`);
});
