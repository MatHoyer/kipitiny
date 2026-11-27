import { Braces, Eye, EyeOff } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";
import { TemplateValue } from "@/features/projects/template-value";
import { Button } from "@kipit-labs/kipitidy/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@kipit-labs/kipitidy/dropdown-menu";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { refsIn } from "@/lib/format";
import { cn } from "@/lib/utils";

export type RefSection = { label: string; items: { label: string; value: string }[] };

/**
 * A value input that inserts references at the cursor, previewing them as
 * chips. A secret is typed blind unless revealed.
 */
export function TemplateInput({
  value,
  onChange,
  secret,
  placeholder,
  sections,
  invalid,
  hint,
}: {
  value: string;
  onChange: (v: string) => void;
  secret: boolean;
  placeholder: string;
  sections: RefSection[];
  invalid: boolean;
  hint?: ReactNode;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [shown, setShown] = useState(false);
  const hidden = secret && !shown;
  const insert = (ref: string) => {
    const el = input.current;
    const start = el?.selectionStart ?? value.length;
    const end = el?.selectionEnd ?? value.length;
    onChange(value.slice(0, start) + ref + value.slice(end));
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(start + ref.length, start + ref.length);
    });
  };
  const hasRefs = refsIn(value).length > 0;

  return (
    <div className="space-y-1.5">
      <div className="flex items-start gap-2">
        <div className="relative min-w-0 flex-1">
          <FloatingInput
            ref={input}
            label="Value"
            type={hidden ? "password" : "text"}
            autoComplete={secret ? "new-password" : "off"}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            spellCheck={false}
            aria-invalid={invalid || undefined}
            inputClassName={cn("font-mono text-sm", secret && "pr-12")}
          />
          {secret && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={shown ? "Hide value" : "Show value"}
              title={shown ? "Hide value" : "Show value"}
              onClick={() => setShown(!shown)}
              className="absolute top-1/2 right-2 -translate-y-1/2"
            >
              {shown ? <EyeOff /> : <Eye />}
            </Button>
          )}
        </div>
        {sections.length > 0 && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button type="button" variant="outline" size="icon" className="mt-3 shrink-0" title="Insert a reference" aria-label="Insert a reference">
                <Braces />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="max-h-80 min-w-48">
              {sections.map((s, n) => (
                <div key={s.label}>
                  {n > 0 && <DropdownMenuSeparator />}
                  <DropdownMenuLabel className="text-xs">{s.label}</DropdownMenuLabel>
                  {s.items.map((item) => (
                    <DropdownMenuItem key={item.value} onSelect={() => insert(item.value)} className="font-mono text-xs">
                      {item.label}
                    </DropdownMenuItem>
                  ))}
                </div>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      {hasRefs && (
        <div className="flex min-h-8 items-center gap-2 rounded-lg bg-muted/50 px-2 py-1 text-xs">
          <span className="shrink-0 text-muted-foreground">Preview</span>
          <TemplateValue value={value} maskText={hidden} />
        </div>
      )}
      {hint && <p className="px-1 text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}
