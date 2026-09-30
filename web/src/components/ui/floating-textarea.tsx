import * as React from "react"
import { cn } from "cn"

type FloatingTextareaProps = Omit<React.ComponentProps<"textarea">, "placeholder"> & {
  label: string
  /** Hint shown once the field is focused and still empty. */
  placeholder?: string
  /** Helper text under the field. */
  description?: React.ReactNode
}

/**
 * Textarea with its label inside, like FloatingInput: it rests on the first
 * line while the field is empty and unfocused, and floats to the top
 * otherwise. The field grows with its content.
 */
function FloatingTextarea({ label, placeholder, description, id, className, ...props }: FloatingTextareaProps) {
  const generatedId = React.useId()
  const textareaId = id ?? generatedId
  const descriptionId = description ? `${textareaId}-description` : undefined

  return (
    <div data-slot="floating-textarea" className={cn("group/field space-y-1.5", className)}>
      <div className="relative">
        <textarea
          id={textareaId}
          // A placeholder is required for :placeholder-shown; it stays invisible until focus.
          placeholder={placeholder ?? " "}
          aria-describedby={descriptionId}
          className={cn(
            "peer field-sizing-content block min-h-24 w-full min-w-0 rounded-xl border border-input bg-transparent px-4 pt-6 pb-2 text-base transition-[color,box-shadow,border-color] outline-none",
            "placeholder:text-transparent focus:placeholder:text-muted-foreground/70",
            "focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50",
            "disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-60 dark:bg-input/30 dark:disabled:bg-input/80",
            "aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40"
          )}
          {...props}
        />
        <label
          htmlFor={textareaId}
          className={cn(
            // Floated position is the default; the resting one applies while empty and unfocused.
            "pointer-events-none absolute top-2 left-4 origin-left text-xs text-muted-foreground transition-all select-none",
            "peer-placeholder-shown:top-4 peer-placeholder-shown:text-base",
            "peer-focus:top-2 peer-focus:text-xs peer-focus:text-foreground",
            "peer-disabled:opacity-60 peer-aria-invalid:text-destructive"
          )}
        >
          {label}
        </label>
      </div>
      {description && (
        <p id={descriptionId} className="px-1 text-xs text-muted-foreground">
          {description}
        </p>
      )}
    </div>
  )
}

export { FloatingTextarea }
