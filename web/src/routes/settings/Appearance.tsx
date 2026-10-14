import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ImageUp, RotateCcw, Trash2 } from "lucide-react";
import { useId, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { toast } from "sonner";
import { Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Logo } from "@/components/logo";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { FloatingInput } from "@/components/ui/floating-input";
import { readableOn, useBranding } from "@/lib/branding";
import { cn } from "@/lib/utils";
import { api, type AuthState, type Branding } from "@/api";
import { SettingsPage } from "./page";

const presets = ["#4f46e5", "#2563eb", "#0891b2", "#059669", "#d97706", "#e11d48", "#7c3aed"];
const maxLogo = 128 << 10;
const hexRe = /^#[0-9a-f]{6}$/i;

/** logo: a data URL picked here, "" once removed, undefined while unchanged. */
type Form = { name: string; accent: string; logo?: string };

const formOf = (b: Branding): Form => ({ name: b.name ?? "", accent: b.accent ?? "" });

export function Appearance() {
  const qc = useQueryClient();
  const saved = useBranding();
  const [form, setForm] = useState<Form>(() => formOf(saved));
  const formId = useId();
  const fileRef = useRef<HTMLInputElement>(null);

  const onSaved = (b: Branding) => {
    qc.setQueryData<AuthState>(["auth"], (a) => a && { ...a, branding: b });
    setForm(formOf(b));
  };
  const save = useMutation({ meta: { error: "Couldn't save the appearance" }, mutationFn: api.setBranding, onSuccess: onSaved });
  const reset = useMutation({ meta: { error: "Couldn't reset the appearance" }, mutationFn: api.resetBranding, onSuccess: onSaved });

  const dirty = form.logo !== undefined || form.name !== (saved.name ?? "") || form.accent !== (saved.accent ?? "");
  const accentOK = form.accent === "" || hexRe.test(form.accent);
  const preview: Branding = {
    name: form.name.trim() || undefined,
    accent: accentOK ? form.accent || undefined : saved.accent,
    logo: form.logo === undefined ? saved.logo : form.logo || undefined,
  };
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm({ ...form, [k]: v });

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
    if (accentOK) save.mutate({ name: form.name.trim(), accent: form.accent.toLowerCase(), logo: form.logo });
  };

  const customized = !!(saved.name || saved.accent || saved.logo);
  const actions = customized && (
    <ConfirmDialog
      trigger={
        <Button variant="outline" size="sm" loading={reset.isPending}>
          <RotateCcw data-icon="inline-start" />
          Reset
        </Button>
      }
      title="Back to kipitiny's look?"
      description="The name, accent colour and logo go back to kipitiny's own."
      confirmLabel="Reset"
      destructive={false}
      onConfirm={() => reset.mutate()}
    />
  );

  return (
    <SettingsPage actions={actions}>
      <Section title="Preview" description="How the sidebar, sign-in page and buttons look once saved. Emails use the name and accent too.">
        <div className="flex flex-wrap items-center justify-between gap-4 rounded-xl border bg-muted/40 p-4">
          <Logo branding={preview} className="text-lg" />
          <span
            className={cn("inline-flex h-8 items-center rounded-lg px-3 text-sm font-medium", !preview.accent && "bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900")}
            style={preview.accent ? { background: preview.accent, color: readableOn(preview.accent) } : undefined}
          >
            Deploy
          </span>
        </div>
      </Section>
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <Section title="Name" description="Shown in the sidebar, on the sign-in page, in the browser tab and in emails.">
          <FloatingInput label="Name" maxLength={40} placeholder="kipitiny" value={form.name} onChange={(e) => set("name", e.target.value)} />
        </Section>
        <Section title="Accent colour" description="Colours buttons, focus rings and the bar on top of emails, in light and dark mode alike.">
          <div className="flex flex-wrap items-center gap-2">
            {presets.map((c) => (
              <button
                key={c}
                type="button"
                title={c}
                aria-label={c}
                onClick={() => set("accent", c)}
                className={cn(
                  "size-8 rounded-full border-2 border-background ring-offset-background outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  form.accent === c && "ring-2 ring-foreground ring-offset-2",
                )}
                style={{ background: c }}
              />
            ))}
            <label className="relative size-8 cursor-pointer overflow-hidden rounded-full border" title="Pick a colour">
              <input
                type="color"
                className="absolute inset-0 size-full cursor-pointer opacity-0"
                value={hexRe.test(form.accent) ? form.accent : "#4f46e5"}
                onChange={(e) => set("accent", e.target.value)}
              />
              <span className="block size-full bg-[conic-gradient(red,yellow,lime,cyan,blue,magenta,red)]" />
            </label>
            {form.accent && (
              <Button type="button" variant="ghost" size="sm" onClick={() => set("accent", "")}>
                Default
              </Button>
            )}
          </div>
          <FloatingInput
            label="Hex colour"
            className="max-w-48"
            placeholder="#4f46e5"
            value={form.accent}
            onChange={(e) => set("accent", e.target.value.trim())}
            aria-invalid={!accentOK}
            description={accentOK ? "Empty keeps kipitiny's black and white." : <span className="text-destructive">Like #4f46e5.</span>}
          />
        </Section>
        <Section title="Logo" description="Square works best: it sits next to the name and becomes the favicon. PNG, JPEG, WebP or SVG, up to 128 KB.">
          <div className="flex flex-wrap items-center gap-3">
            <div className="flex size-14 items-center justify-center rounded-xl border bg-muted/40">
              {preview.logo ? <img src={preview.logo} alt="" className="size-10 object-contain" /> : <ImageUp className="size-5 text-muted-foreground" />}
            </div>
            <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp,image/svg+xml" className="hidden" onChange={onFile} />
            <Button type="button" variant="outline" size="sm" onClick={() => fileRef.current?.click()}>
              <ImageUp data-icon="inline-start" />
              {preview.logo ? "Replace" : "Upload"}
            </Button>
            {preview.logo && (
              <Button type="button" variant="ghost" size="sm" className="text-muted-foreground hover:text-destructive" onClick={() => set("logo", "")}>
                <Trash2 data-icon="inline-start" />
                Remove
              </Button>
            )}
          </div>
        </Section>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(formOf(saved))} />
    </SettingsPage>
  );
}
