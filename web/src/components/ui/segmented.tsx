import * as React from "react"
import { cn } from "cn"
import { ToggleGroup as ToggleGroupPrimitive } from "radix-ui"

/**
 * Pick one of a few options, drawn like the default tabs: a muted track with
 * the chosen option raised. Selection is styled from aria-checked, which a
 * wrapping tooltip trigger can't overwrite (it does data-state).
 */
function Segmented({
  className,
  size = "default",
  onValueChange,
  ...props
}: Omit<React.ComponentProps<typeof ToggleGroupPrimitive.Root>, "type" | "value" | "defaultValue" | "onValueChange"> & {
  value: string
  onValueChange: (value: string) => void
  size?: "default" | "sm"
}) {
  return (
    <ToggleGroupPrimitive.Root
      type="single"
      data-slot="segmented"
      data-size={size}
      // Clicking the chosen option would clear it: a segmented control always holds one.
      onValueChange={(v) => v && onValueChange(v)}
      className={cn(
        "group/segmented inline-flex w-fit max-w-full items-center rounded-lg bg-muted p-[3px] text-muted-foreground data-[size=default]:h-9 data-[size=sm]:h-8",
        className
      )}
      {...props}
    />
  )
}

function SegmentedItem({ className, ...props }: React.ComponentProps<typeof ToggleGroupPrimitive.Item>) {
  return (
    <ToggleGroupPrimitive.Item
      data-slot="segmented-item"
      className={cn(
        "inline-flex h-full shrink-0 items-center justify-center gap-1.5 rounded-md border border-transparent px-2.5 text-sm font-medium whitespace-nowrap text-foreground/60 transition-all outline-none group-data-[size=sm]/segmented:px-2 group-data-[size=sm]/segmented:text-xs hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 dark:text-muted-foreground dark:hover:text-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        "aria-checked:bg-background aria-checked:text-foreground aria-checked:shadow-sm dark:aria-checked:border-input dark:aria-checked:bg-input/30 dark:aria-checked:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export { Segmented, SegmentedItem }
