import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ImageUp, RotateCcw, Trash2 } from "lucide-react";
import { useContext, useEffect, useId, useRef, useState, type ChangeEvent, type CSSProperties, type FormEvent } from "react";
import { toast } from "sonner";
import { Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Logo } from "@/components/logo";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { BrandingPreview, fonts, radii, useBranding } from "@/lib/branding";
import { cn } from "@/lib/utils";
import { api, type AuthState, type Branding, type Theme } from "@/api";
import { SettingsPage } from "./page";

const maxLogo = 128 << 10;
const hexRe = /^#[0-9a-f]{6}$/i;
const swatches = ["#4f46e5", "#2563eb", "#0891b2", "#059669", "#d97706", "#e11d48", "#7c3aed"];

/** Complete looks to start from: picking one fills the theme, then any field can be tweaked. */
const presets: { label: string; theme: Theme }[] = [
  { label: "kipitiny", theme: {} },
  { label: "Nord", theme: { accent: "#5e81ac", accentDark: "#88c0d0", tint: "#4c566a" } },
  { label: "Solarized", theme: { accent: "#268bd2", accentDark: "#2aa198", tint: "#b58900", radius: "small" } },
  { label: "Forest", theme: { accent: "#15803d", accentDark: "#4ade80", tint: "#166534", radius: "large" } },
  { label: "Midnight", theme: { accent: "#4f46e5", accentDark: "#818cf8", tint: "#312e81", radius: "large" } },
  { label: "Rose", theme: { accent: "#e11d48", accentDark: "#fb7185", tint: "#be123c", radius: "full", font: "system" } },
  { label: "Paper", theme: { accent: "#9a3412", accentDark: "#fdba74", tint: "#a16207", radius: "small", font: "serif" } },
  { label: "Terminal", theme: { accent: "#15803d", accentDark: "#4ade80", radius: "none", font: "mono", density: "compact" } },
];

/** Every field as a string, "" for kipitiny's own. */
type FormTheme = { [K in keyof Theme]-?: string };
/** logo: a data URL picked here, "" once removed, undefined while unchanged. */
type Form = FormTheme & { name: string; logo?: string };

const themeKeys = ["accent", "accentDark", "tint", "radius", "font", "density"] as const;
const colorKeys = ["accent", "accentDark", "tint"] as const;
const defaults: Partial<FormTheme> = { radius: "medium", font: "geist", density: "comfortable" };

const formTheme = (t: Theme): FormTheme => Object.fromEntries(themeKeys.map((k) => [k, t[k] ?? ""])) as FormTheme;
const formOf = (b: Branding): Form => ({ name: b.name ?? "", ...formTheme(b) });
/** The form's theme as saved: defaults and unfinished colours dropped. */
const themeOf = (f: FormTheme): Theme =>
  Object.fromEntries(
    themeKeys.map((k) => {
      const v = f[k];
      const unset = !v || v === defaults[k] || ((colorKeys as readonly string[]).includes(k) && !hexRe.test(v));
      return [k, unset ? undefined : v];
    }),
  ) as Theme;
const sameTheme = (a: Theme, b: Theme) => themeKeys.every((k) => (a[k] ?? "") === (b[k] ?? ""));

export function Appearance() {
  const qc = useQueryClient();
  const saved = useBranding();
  const setPreview = useContext(BrandingPreview);
  const [form, setForm] = useState<Form>(() => formOf(saved));
  const formId = useId();
  const fileRef = useRef<HTMLInputElement>(null);

  const onSaved = (b: Branding) => {
    qc.setQueryData<AuthState>(["auth"], (a) => a && { ...a, branding: b });
    setForm(formOf(b));
  };
  const save = useMutation({ meta: { error: "Couldn't save the appearance" }, mutationFn: api.setBranding, onSuccess: onSaved });
  const reset = useMutation({ meta: { error: "Couldn't reset the appearance" }, mutationFn: api.resetBranding, onSuccess: onSaved });

  const colorsOK = colorKeys.every((k) => form[k] === "" || hexRe.test(form[k]));
  const theme = themeOf(form);
  const preview: Branding = {
    ...theme,
    name: form.name.trim() || undefined,
    logo: form.logo === undefined ? saved.logo : form.logo || undefined,
  };
  const dirty = form.logo !== undefined || form.name.trim() !== (saved.name ?? "") || !sameTheme(theme, saved);

  // The whole UI shows the unsaved theme while it's edited here.
  const previewKey = JSON.stringify(preview);
  useEffect(() => {
    setPreview(dirty ? (JSON.parse(previewKey) as Branding) : null);
  }, [dirty, previewKey, setPreview]);
  useEffect(() => () => setPreview(null), [setPreview]);

  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => ({ ...f, [k]: v }));
  const onFile = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    if (file.size > maxLogo) {
      toast.error("Logo too big", { description: `At most ${maxLogo >> 10} KB; an SVG is usually smallest.` });
      return;
    }
    const reader = new FileReader();
    reader.onload = () => set("logo", reader.result as string);
    reader.readAsDataURL(file);
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (colorsOK) save.mutate({ ...theme, name: form.name.trim(), logo: form.logo });
  };

  const customized = !!saved.name || !!saved.logo || !sameTheme(saved, {});
  const actions = customized && (
    <ConfirmDialog
      trigger={
        <Button variant="outline" size="sm" loading={reset.isPending}>
          <RotateCcw data-icon="inline-start" />
          Reset
        </Button>
      }
      title="Back to kipitiny's look?"
      description="The name, logo and theme go back to kipitiny's own."
      confirmLabel="Reset"
      destructive={false}
      onConfirm={() => reset.mutate()}
    />
  );

  return (
    <SettingsPage actions={actions}>
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <Section title="Theme" description="Start from a look, then adjust it below. The whole page shows your changes until you save or leave.">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            {presets.map((p) => (
              <PresetTile
                key={p.label}
                label={p.label}
                theme={p.theme}
                selected={sameTheme(theme, p.theme)}
                onClick={() => setForm((f) => ({ ...f, ...formTheme(p.theme) }))}
              />
            ))}
          </div>
        </Section>
        <Section title="Name and logo" description="Shown in the sidebar, on the sign-in page and in the browser tab; emails use the name too.">
          <div className="flex flex-wrap items-center justify-between gap-4 rounded-xl border bg-muted/40 p-4">
            <Logo branding={preview} className="text-lg" />
            <div className="flex gap-2">
              <Button type="button" size="sm" tabIndex={-1}>
                Deploy
              </Button>
              <Button type="button" size="sm" variant="outline" tabIndex={-1}>
                Logs
              </Button>
            </div>
          </div>
          <FloatingInput label="Name" maxLength={40} placeholder="kipitiny" value={form.name} onChange={(e) => set("name", e.target.value)} />
          <div className="flex flex-wrap items-center gap-3">
            <div className="flex size-14 items-center justify-center rounded-xl border bg-muted/40">
              {preview.logo ? <img src={preview.logo} alt="" className="size-10 object-contain" /> : <ImageUp className="size-5 text-muted-foreground" />}
            </div>
            <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp,image/svg+xml" className="hidden" onChange={onFile} />
            <Button type="button" variant="outline" size="sm" onClick={() => fileRef.current?.click()}>
              <ImageUp data-icon="inline-start" />
              {preview.logo ? "Replace logo" : "Upload logo"}
            </Button>
            {preview.logo && (
              <Button type="button" variant="ghost" size="sm" className="text-muted-foreground hover:text-destructive" onClick={() => set("logo", "")}>
                <Trash2 data-icon="inline-start" />
                Remove
              </Button>
            )}
            <p className="w-full text-xs text-muted-foreground">Square works best: it also becomes the favicon and the app icon. PNG, JPEG, WebP or SVG, up to 128 KB.</p>
          </div>
        </Section>
        <Section title="Colours">
          <div className="grid gap-6 lg:grid-cols-3">
            <ColorField
              label="Accent"
              description="Buttons, focus rings and the bar on top of emails."
              emptyLabel="Black and white"
              value={form.accent}
              onChange={(v) => set("accent", v)}
            />
            <ColorField
              label="Accent in dark mode"
              description="A lighter shade reads better on dark backgrounds."
              emptyLabel="Same as light"
              value={form.accentDark}
              onChange={(v) => set("accentDark", v)}
            />
            <ColorField
              label="Tint"
              description="Mixed lightly into backgrounds, surfaces and borders."
              emptyLabel="Neutral grey"
              value={form.tint}
              onChange={(v) => set("tint", v)}
            />
          </div>
        </Section>
        <Section title="Shape and type">
          <div className="grid gap-6 lg:grid-cols-3">
            <Choice
              label="Corners"
              value={form.radius || "medium"}
              onChange={(v) => set("radius", v)}
              options={[
                ["none", "Square"],
                ["small", "S"],
                ["medium", "M"],
                ["large", "L"],
                ["full", "Round"],
              ]}
            />
            <Choice
              label="Font"
              value={form.font || "geist"}
              onChange={(v) => set("font", v)}
              options={[
                ["geist", "Geist"],
                ["system", "System"],
                ["serif", "Serif"],
                ["mono", "Mono"],
              ]}
              itemStyle={(v) => ({ fontFamily: fonts[v as keyof typeof fonts] })}
            />
            <Choice
              label="Density"
              value={form.density || "comfortable"}
              onChange={(v) => set("density", v)}
              options={[
                ["comfortable", "Comfortable"],
                ["compact", "Compact"],
              ]}
            />
          </div>
        </Section>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(formOf(saved))} />
    </SettingsPage>
  );
}

/** A preset drawn in its own colours, corners and font: light mode on the left, dark on the right. */
function PresetTile({ label, theme, selected, onClick }: { label: string; theme: Theme; selected: boolean; onClick: () => void }) {
  const radius = radii[theme.radius ?? "medium"];
  const light = theme.tint ? `color-mix(in oklab, ${theme.tint} 10%, white)` : "#fafafa";
  const dark = theme.tint ? `color-mix(in oklab, ${theme.tint} 14%, #18181b)` : "#27272a";
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={cn(
        "flex flex-col gap-2 rounded-xl border p-2 text-left transition-colors outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring",
        selected && "border-primary ring-1 ring-primary",
      )}
    >
      <div className="grid h-14 grid-cols-2 overflow-hidden rounded-lg border">
        <div className="flex items-end p-2" style={{ background: light }}>
          <span className="h-3 w-8" style={{ background: theme.accent ?? "#18181b", borderRadius: `calc(${radius} * 0.5)` }} />
        </div>
        <div className="flex items-end p-2" style={{ background: dark }}>
          <span className="h-3 w-8" style={{ background: theme.accentDark ?? theme.accent ?? "#fafafa", borderRadius: `calc(${radius} * 0.5)` }} />
        </div>
      </div>
      <span className="flex items-center justify-between px-0.5 text-sm font-medium" style={{ fontFamily: fonts[theme.font ?? "geist"] }}>
        {label}
        {selected && <Check className="size-4 text-primary" />}
      </span>
    </button>
  );
}

function ColorField({
  label,
  description,
  emptyLabel,
  value,
  onChange,
}: {
  label: string;
  description: string;
  emptyLabel: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const ok = value === "" || hexRe.test(value);
  return (
    <div className="space-y-3">
      <div>
        <p className="text-sm font-medium">{label}</p>
        <p className="text-xs text-muted-foreground">{description}</p>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <button
          type="button"
          title={emptyLabel}
          aria-label={emptyLabel}
          onClick={() => onChange("")}
          className={cn(
            "size-7 rounded-full border bg-[linear-gradient(135deg,#fafafa_50%,#18181b_50%)] ring-offset-2 ring-offset-background outline-none focus-visible:ring-2 focus-visible:ring-ring",
            value === "" && "ring-2 ring-foreground",
          )}
        />
        {swatches.map((c) => (
          <button
            key={c}
            type="button"
            title={c}
            aria-label={c}
            onClick={() => onChange(c)}
            className={cn(
              "size-7 rounded-full ring-offset-2 ring-offset-background outline-none focus-visible:ring-2 focus-visible:ring-ring",
              value === c && "ring-2 ring-foreground",
            )}
            style={{ background: c }}
          />
        ))}
        <label className="relative size-7 cursor-pointer overflow-hidden rounded-full border" title="Pick a colour">
          <input
            type="color"
            className="absolute inset-0 size-full cursor-pointer opacity-0"
            value={hexRe.test(value) ? value : "#4f46e5"}
            onChange={(e) => onChange(e.target.value)}
          />
          <span className="block size-full bg-[conic-gradient(red,yellow,lime,cyan,blue,magenta,red)]" />
        </label>
      </div>
      <FloatingInput
        label="Hex"
        size="sm"
        placeholder={emptyLabel}
        value={value}
        onChange={(e) => onChange(e.target.value.trim().toLowerCase())}
        aria-invalid={!ok}
        description={ok ? (value ? undefined : emptyLabel) : <span className="text-destructive">Like #4f46e5.</span>}
      />
    </div>
  );
}

function Choice({
  label,
  value,
  onChange,
  options,
  itemStyle,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: [string, string][];
  itemStyle?: (v: string) => CSSProperties;
}) {
  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">{label}</p>
      <Segmented value={value} onValueChange={onChange} aria-label={label}>
        {options.map(([v, l]) => (
          <SegmentedItem key={v} value={v} style={itemStyle?.(v)}>
            {l}
          </SegmentedItem>
        ))}
      </Segmented>
    </div>
  );
}
