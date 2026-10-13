import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Send, Trash2 } from "lucide-react";
import { useEffect, useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { DangerZone, ErrorText, Loading, Section } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SaveBar } from "@/components/save-bar";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api, type Email as EmailState, type EmailSecurity, type EmailSettings } from "@/api";
import { SettingsPage } from "./page";

const securities: { value: EmailSecurity; label: string; port: number }[] = [
  { value: "starttls", label: "STARTTLS", port: 587 },
  { value: "tls", label: "TLS", port: 465 },
  { value: "none", label: "None (local relay)", port: 25 },
];

export function Email() {
  const qc = useQueryClient();
  const email = useQuery({ queryKey: ["email"], queryFn: api.email });
  const [form, setForm] = useState<EmailSettings | null>(null);
  const [testing, setTesting] = useState(false);
  useEffect(() => {
    if (email.data && !form) setForm(email.data.settings);
  }, [email.data, form]);

  const onSaved = (v: EmailState) => {
    qc.setQueryData(["email"], v);
    setForm(v.settings);
  };
  const save = useMutation({ meta: { error: "Couldn't save the SMTP server" }, mutationFn: api.setEmail, onSuccess: onSaved });
  const remove = useMutation({
    meta: { error: "Couldn't remove the SMTP server" },
    mutationFn: api.deleteEmail,
    onSuccess: () => {
      setForm(null);
      qc.invalidateQueries({ queryKey: ["email"] });
    },
  });

  const formId = useId();
  const configured = !!email.data?.configured;
  const dirty = !!form && JSON.stringify(form) !== JSON.stringify(email.data?.settings);
  const actions = configured && (
    <Button variant="outline" size="sm" disabled={dirty} title={dirty ? "Save first" : undefined} onClick={() => setTesting(true)}>
      <Send data-icon="inline-start" />
      Send a test
    </Button>
  );
  if (!form) return <SettingsPage>{email.error ? <ErrorText error={email.error} /> : <Loading />}</SettingsPage>;

  const set = <K extends keyof EmailSettings>(k: K, v: EmailSettings[K]) => setForm({ ...form, [k]: v });
  // Switching security moves a default port along; without TLS there's no sign-in.
  const setSecurity = (security: EmailSecurity) => {
    const port = securities.some((s) => s.port === form.port) ? securities.find((s) => s.value === security)!.port : form.port;
    setForm({ ...form, security, port, ...(security === "none" && { username: "", password: "" }) });
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(form);
  };

  return (
    <SettingsPage actions={actions}>
      <form id={formId} onSubmit={onSubmit} className="space-y-6">
        <Section
          title="SMTP server"
          description="Your provider's relay (Amazon SES, Postmark, Resend, Gmail…). Email notification channels and invites go through it. Saving checks kipitiny can connect and sign in."
        >
          <div className="grid gap-4 sm:grid-cols-[1fr_8rem_12rem]">
            <FloatingInput
              label="Host"
              required
              value={form.host}
              onChange={(e) => set("host", e.target.value)}
              placeholder="smtp.example.com"
            />
            <FloatingInput
              label="Port"
              type="number"
              min={1}
              max={65535}
              required
              value={String(form.port)}
              onChange={(e) => set("port", Number(e.target.value) || 0)}
            />
            <FloatingSelect label="Security" value={form.security} onValueChange={(v) => setSecurity(v as EmailSecurity)} options={securities} />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <FloatingInput
              label="Username"
              autoComplete="off"
              value={form.username}
              disabled={form.security === "none"}
              onChange={(e) => set("username", e.target.value)}
              description={form.security === "none" ? "Signing in needs TLS or STARTTLS." : "Empty when the server needs no sign-in."}
            />
            <FloatingInput
              label="Password"
              type="password"
              autoComplete="new-password"
              value={form.password}
              disabled={form.security === "none"}
              onChange={(e) => set("password", e.target.value)}
            />
          </div>
          <FloatingInput
            label="Sender"
            required
            value={form.from}
            onChange={(e) => set("from", e.target.value)}
            placeholder="kipitiny <ops@example.com>"
            description="An address your provider lets you send from, with an optional name."
          />
        </Section>
      </form>
      <SaveBar form={formId} dirty={dirty} saving={save.isPending} onReset={() => setForm(email.data!.settings)} />
      {configured && (
        <DangerZone description="Email channels and invites stop sending until a server is set again.">
          <ConfirmDialog
            trigger={
              <Button variant="destructive" size="sm" loading={remove.isPending}>
                <Trash2 data-icon="inline-start" />
                Remove
              </Button>
            }
            title="Remove the SMTP server?"
            confirmLabel="Remove"
            onConfirm={() => remove.mutate()}
          />
        </DangerZone>
      )}
      <Dialog open={testing} onOpenChange={setTesting}>
        <DialogContent className="sm:max-w-md">{testing && <TestForm onDone={() => setTesting(false)} />}</DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function TestForm({ onDone }: { onDone: () => void }) {
  const [to, setTo] = useState("");
  const test = useMutation({
    meta: { error: "The test email wasn't sent" },
    mutationFn: () => api.testEmail(to),
    onSuccess: () => {
      toast.success(`Test email sent to ${to}`);
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    test.mutate();
  };
  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>Send a test email</DialogTitle>
        <DialogDescription>Check it arrives, and not in spam: if it does, your provider may need SPF or DKIM set up for the sender's domain.</DialogDescription>
      </DialogHeader>
      <FloatingInput label="To" type="email" required autoFocus value={to} onChange={(e) => setTo(e.target.value)} placeholder="you@example.com" />
      <DialogFooter>
        <Button type="submit" loading={test.isPending}>
          <Send data-icon="inline-start" />
          Send
        </Button>
      </DialogFooter>
    </form>
  );
}
