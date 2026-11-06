# Homelab design system: how to build with it

Neutral, compact, shadcn-based UI. Monochrome primary, soft rounded corners, Geist for UI text, colour only for status. No logo ships here: products use a plain text wordmark.

## Setup

- Everything lives on `window.HomelabUI`, including subparts (`CardHeader`, `DialogContent`, `SidebarMenuButton`), every `lucide-react` icon (`Plus`, `Trash2`) and sonner's `toast`.
- Wrap each screen once in `<TooltipProvider>`, or `Tooltip` and `Tip` throw.
- Mount `<Toaster />` once when using `toast.success(...)` or `toast.error(...)`.
- Dark mode: add class `dark` to `<html>` or a wrapper. Do not write `dark:` overrides for colours, tokens already flip.

## Prefer these components

- Forms: `FloatingInput`, `FloatingTextarea`, `FloatingSelect` (label inside the field) over `Label` + `Input`. Use the plain pair only in dense settings rows.
- Tooltips: `<Tip content="...">` around one element, not the `Tooltip` parts and never a native `title`.
- Tab-like switch of a view: `Segmented`, or `Tabs` with panels. Multi-step progress: `Stepper`. Waiting: `Spinner`, `Skeleton`, or `loading` on the button.
- Destructive actions: `Button variant="destructive"` inside `AlertDialog`. Side forms: `Sheet`. Short forms: `Dialog`.

## Styling vocabulary

Tailwind utilities are compiled ahead of time, so stick to what exists; arbitrary values such as `w-[437px]` will not apply.

- Layout: `flex`, `inline-flex`, `grid`, `flex-col`, `items-center`, `justify-between`, `gap-*`, `space-y-*`, `p-*`, `px-*`, `py-*`, `m*-*`, `grid-cols-1` to `6`, with `sm:`, `md:`, `lg:`.
- Size: `w-full`, `size-4` to `size-64`, `max-w-xs` to `max-w-6xl`.
- Colour tokens only, never raw hex: `bg-background`, `bg-card`, `bg-muted`, `bg-primary`, `text-foreground`, `text-muted-foreground`, `text-primary-foreground`, `text-destructive`, `border-border`.
- CSS variables for custom CSS: `--background`, `--foreground`, `--card`, `--primary`, `--muted`, `--border`, `--ring`, `--destructive`, `--radius`.
- Type: `font-sans` (Geist) for UI, `font-mono` (Geist Mono) for values and code, `font-display` (Bricolage Grotesque) for rare big headings. `text-xs`, `text-sm` are the usual body sizes.

## Tone

Calm and sparse: no gradients, no coloured panels, no decorative shadows. Sentence case labels. Lean on `Card` for grouping and `Separator` for division.

## Example

```jsx
const { TooltipProvider, Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter, FloatingInput, Button, Tip, Plus } = window.HomelabUI;

<TooltipProvider>
  <Card className="max-w-md">
    <CardHeader>
      <CardTitle>Add a domain</CardTitle>
      <CardDescription>Point its DNS at this server first.</CardDescription>
    </CardHeader>
    <CardContent className="grid gap-4">
      <FloatingInput label="Domain" defaultValue="app.example.com" />
    </CardContent>
    <CardFooter className="justify-end gap-2">
      <Button variant="outline">Cancel</Button>
      <Tip content="Issues a certificate"><Button><Plus /> Add domain</Button></Tip>
    </CardFooter>
  </Card>
</TooltipProvider>
```
