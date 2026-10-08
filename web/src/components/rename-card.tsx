import { useState, type FormEvent, type ReactNode } from "react";
import { Section } from "@/components/common";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";

/** Renames a project or a service: lowercase letters, digits and dashes. */
export function RenameCard({
  name,
  description,
  disabled,
  pending,
  onRename,
}: {
  name: string;
  description: ReactNode;
  disabled?: boolean;
  pending: boolean;
  onRename: (name: string) => void;
}) {
  const [value, setValue] = useState(name);
  const next = value.trim();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (next && next !== name) onRename(next);
  };
  return (
    <Section title="Name" description={description}>
      <form onSubmit={submit} className="flex items-center gap-2">
        <FloatingInput
          label="Name"
          className="w-full max-w-sm"
          autoComplete="off"
          pattern="[a-z0-9]([a-z0-9\-]{0,38}[a-z0-9])?"
          title="Lowercase letters, digits and dashes (max 40)"
          disabled={disabled}
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        <Button type="submit" variant="outline" disabled={disabled || !next || next === name} loading={pending}>
          Rename
        </Button>
      </form>
    </Section>
  );
}
