import * as React from "react"
import { cn } from "cn"
import { Select as SelectPrimitive } from "radix-ui"
import { ChevronDownIcon } from "lucide-react"

import { SelectContent, SelectItem } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"

type Option = { value: string; label: React.ReactNode }

type FloatingSelectProps = {
  label: string
  value: string
  onValueChange: (value: string) => void
  options: Option[]
  /** Helper text under the field. */
  description?: React.ReactNode
  disabled?: boolean
  /** Options are still loading: a spinner replaces the chevron. */
  loading?: boolean
  id?: string
  className?: string
}

/**
 * Select styled like FloatingInput. A select always shows a value, so its
 * label stays floated at the top.
 */
function FloatingSelect({
  label,
  value,
  onValueChange,
  options,
  description,
  disabled,
  loading,
  id,
  className,
}: FloatingSelectProps) {
  const generatedId = React.useId()
  const triggerId = id ?? generatedId
  const descriptionId = description ? `${triggerId}-description` : undefined

  return (
    <div data-slot="floating-select" className={cn("group/field space-y-1.5", className)}>
      <SelectPrimitive.Root value={value} onValueChange={onValueChange} disabled={disabled}>
        <div className="relative">
          <SelectPrimitive.Trigger
            id={triggerId}
            aria-describedby={descriptionId}
            className={cn(
              "peer flex h-14 w-full min-w-0 items-center justify-between gap-2 rounded-xl border border-input bg-transparent px-4 pt-5 pb-1.5 text-left text-base transition-[color,box-shadow,border-color] outline-none",
              "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50",
              "disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-60 dark:bg-input/30 dark:hover:bg-input/50 dark:disabled:bg-input/80",
              "*:data-[slot=select-value]:line-clamp-1"
            )}
          >
            <SelectPrimitive.Value data-slot="select-value" />
            <SelectPrimitive.Icon asChild>
              {loading ? (
                <Spinner className="pointer-events-none -mt-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <ChevronDownIcon className="pointer-events-none -mt-3.5 size-4 shrink-0 text-muted-foreground" />
              )}
            </SelectPrimitive.Icon>
          </SelectPrimitive.Trigger>
          <label
            htmlFor={triggerId}
            className="pointer-events-none absolute top-2 left-4 text-xs text-muted-foreground select-none peer-focus-visible:text-foreground peer-disabled:opacity-60"
          >
            {label}
          </label>
        </div>
        <SelectContent className="rounded-xl">
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value} className="rounded-md py-2">
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </SelectPrimitive.Root>
      {description && (
        <p id={descriptionId} className="px-1 text-xs text-muted-foreground">
          {description}
        </p>
      )}
    </div>
  )
}

export { FloatingSelect }
export type { Option as FloatingSelectOption }
