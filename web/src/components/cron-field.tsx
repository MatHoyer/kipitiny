import { useState } from "react";
import { Mono } from "@/components/common";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";

export type CronPreset = [cron: string, label: string];

export const backupPresets: CronPreset[] = [
  ["0 * * * *", "Every hour"],
  ["0 */6 * * *", "Every 6 hours"],
  ["0 3 * * *", "Daily at 03:00 UTC"],
  ["0 3 * * 0", "Weekly, Sunday 03:00 UTC"],
  ["0 3 1 * *", "Monthly, on the 1st at 03:00 UTC"],
];

export const cleanupPresets: CronPreset[] = [
  ["0 5 * * *", "Daily at 05:00 UTC"],
  ["0 5 * * 0", "Weekly, Sunday 05:00 UTC"],
  ["0 5 1 * *", "Monthly, on the 1st at 05:00 UTC"],
];

export function describeCron(cron: string, presets: CronPreset[] = backupPresets): string {
  return presets.find(([c]) => c === cron)?.[1] ?? cron;
}

/**
 * A preset select, plus a cron input when "Custom" is picked or the value
 * matches no preset. Renders two siblings so they sit in the caller's grid.
 */
export function CronField({
  value,
  onChange,
  presets = backupPresets,
  label = "When",
  inputClassName,
}: {
  value: string;
  onChange: (cron: string) => void;
  presets?: CronPreset[];
  label?: string;
  /** Classes for the custom input's wrapper, e.g. a column span. */
  inputClassName?: string;
}) {
  const isPreset = presets.some(([c]) => c === value);
  const [picked, setPicked] = useState(!isPreset);
  const custom = picked || !isPreset;

  return (
    <>
      <FloatingSelect
        label={label}
        value={custom ? "custom" : value}
        onValueChange={(v) => {
          setPicked(v === "custom");
          if (v !== "custom") onChange(v);
        }}
        options={[...presets.map(([value, label]) => ({ value, label })), { value: "custom", label: "Custom cron…" }]}
      />
      {custom && (
        <FloatingInput
          label="Cron expression"
          required
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="30 2 * * 1-5"
          inputClassName="font-mono"
          className={inputClassName}
          description={
            <>
              5 fields, UTC. Prefix with <Mono>CRON_TZ=Europe/Paris</Mono> for another zone.
            </>
          }
        />
      )}
    </>
  );
}
