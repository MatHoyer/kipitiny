import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, Mail, Pencil, Plus, Send, Trash2, UserPlus } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";
import { ResendIcon } from "@/components/brand-icons";
import { ChoiceTile, EmptyState, ErrorText, Mono, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { HowTo, type HowToStep } from "@/components/how-to";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api, type EmailProvider as Provider, type EmailSecurity, type EmailSettings } from "@/api";
import { SettingsPage } from "./page";

const securities: { value: EmailSecurity; label: string; port: number }[] = [
  { value: "starttls", label: "STARTTLS", port: 587 },
  { value: "tls", label: "TLS", port: 465 },
  { value: "none", label: "None (local relay)", port: 25 },
];

/**
 * A known email service: its server is fixed, so the form only asks for its
 * API key and the sender. "other" takes any SMTP server.
 */
type EmailService = {
  id: "resend" | "other";
  label: string;
  icon: ReactNode;
  description: string;
  help: ReactNode;
  /** Brand color, tinting the guide. */
  accent?: string;
  /** The SMTP settings this provider always uses. */
  server?: Pick<EmailSettings, "host" | "port" | "security" | "username">;
  /** What the password is called there. */
  secret?: { label: string; placeholder: string };
  /** How to get the sender's domain and a key, for a given sender. */
  guide?: (domain: string) => HowToStep[];
};

const services: EmailService[] = [
  {
    id: "resend",
    label: "Resend",
    icon: <ResendIcon />,
    description: "Email API for developers, with an SMTP relay.",
    help: (
      <>
        Sends through <Mono>smtp.resend.com</Mono> with TLS, signed in with an API key. The sender must be on a domain verified in Resend.
      </>
    ),
    server: { host: "smtp.resend.com", port: 465, security: "tls", username: "resend" },
    secret: { label: "API key", placeholder: "re_…" },
    guide: (domain) => [
      {
        title: "Verify your domain",
        text: (
          <>
            Domains › Add Domain{domain && <> with <Mono>{domain}</Mono></>}, then add the DNS records Resend shows (SPF and DKIM) where
            your DNS is hosted. Wait until the domain shows Verified.
          </>
        ),
        link: { href: "https://resend.com/domains", label: "Open Domains" },
      },
      {
        title: "Create an API key",
        text: (
          <>
            API Keys › Create API Key, with <Mono>Sending access</Mono> on that domain only. Resend shows it once.
          </>
        ),
        link: { href: "https://resend.com/api-keys", label: "Open API Keys" },
      },
      {
        title: "Paste it below",
        text: (
          <>
            With a sender on the verified domain, e.g. <Mono>kipitiny &lt;alerts@{domain || "example.com"}&gt;</Mono>.
          </>
        ),
      },
    ],
  },
  {
    id: "other",
    label: "Other SMTP server",
    icon: <Mail />,
    description: "Amazon SES, Postmark, Gmail, Office 365, your own relay…",
    help: "Your provider's SMTP host, port and credentials.",
  },
];

const blank: EmailSettings = { name: "", host: "", port: 587, security: "starttls", username: "", password: "", from: "" };

/** The service whose server these settings use. */
const serviceOf = (s: EmailSettings) => services.find((p) => p.server?.host === s.host) ?? services.at(-1)!;

/** The sender's domain, for the guide. */
const senderDomain = (from: string) => /@([^\s>]+)/.exec(from)?.[1] ?? "";

/** What the dialog shows: the service picker, or the form of a service; saved is the provider being edited. */
type Editing = { step: "pick"; saved?: Provider } | { step: "form"; service: EmailService; saved?: Provider };

export function Email() {
  const qc = useQueryClient();
  const email = useQuery({ queryKey: ["email"], queryFn: api.email });
  const [editing, setEditing] = useState<Editing | null>(null);
  const [testing, setTesting] = useState<Provider | null>(null);
  const done = () => qc.invalidateQueries({ queryKey: ["email"] });
  const remove = useMutation({
    meta: { error: "Couldn't remove the email provider" },
    mutationFn: api.deleteEmailProvider,
    onSuccess: done,
  });
  const invites = useMutation({
    meta: { error: "Couldn't change the invites provider" },
    mutationFn: api.setEmailInvites,
    onSuccess: (v) => qc.setQueryData(["email"], v),
  });
  const data = email.data;
  const add = (
    <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
      <Plus data-icon="inline-start" />
      Add provider
    </Button>
  );

  return (
    <SettingsPage actions={data && add}>
      <ErrorText error={email.error} />
      {data?.providers.length === 0 && (
        <EmptyState
          icon={Mail}
          title="No email provider"
          description="Send notifications and invites by email, through Resend or any SMTP server. Add several to send each through its own."
          action={add}
        />
      )}
      {!!data?.providers.length && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {data.providers.map((p) => (
            <EmailCard
              key={p.id}
              provider={p}
              invites={p.id === data.invites}
              onTest={() => setTesting(p)}
              onInvites={() => invites.mutate(p.id)}
              onEdit={() => setEditing({ step: "form", service: serviceOf(p), saved: p })}
              onRemove={() => remove.mutate(p.id)}
            />
          ))}
        </div>
      )}
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
          {editing?.step === "pick" && (
            <>
              <DialogHeader>
                <DialogTitle>{editing.saved ? `Change ${editing.saved.name}` : "Add an email provider"}</DialogTitle>
                <DialogDescription>Where should kipitiny send email from?</DialogDescription>
              </DialogHeader>
              <div className="grid gap-3 sm:grid-cols-2">
                {services.map((p) => (
                  <ChoiceTile
                    key={p.id}
                    icon={p.icon}
                    title={p.label}
                    description={p.description}
                    onClick={() => setEditing({ step: "form", service: p, saved: editing.saved })}
                  />
                ))}
              </div>
            </>
          )}
          {editing?.step === "form" && (
            <EmailForm
              key={editing.service.id}
              service={editing.service}
              editing={editing.saved}
              onBack={() => setEditing({ step: "pick", saved: editing.saved })}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
      <Dialog open={testing !== null} onOpenChange={(o) => !o && setTesting(null)}>
        <DialogContent className="sm:max-w-md">{testing && <TestForm provider={testing} onDone={() => setTesting(null)} />}</DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function EmailCard({
  provider: s,
  invites,
  onTest,
  onInvites,
  onEdit,
  onRemove,
}: {
  provider: Provider;
  /** Invites go through it. */
  invites: boolean;
  onTest: () => void;
  onInvites: () => void;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const p = serviceOf(s);
  const security = securities.find((x) => x.value === s.security)?.label ?? s.security;
  return (
    <Card size="sm" className="gap-3 px-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{p.icon}</span>
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-2 font-medium">
            <span className="truncate">{s.name}</span>
            {invites && <Tag>Invites</Tag>}
          </p>
          <p className="truncate text-xs text-muted-foreground" title={s.from}>
            {s.from}
          </p>
        </div>
      </div>
      <p className="truncate font-mono text-xs text-muted-foreground">
        {s.host}:{s.port} · {security}
      </p>
      <div className="mt-auto flex justify-end gap-0.5">
        <Button variant="ghost" size="icon-sm" title="Send a test" aria-label="Send a test" onClick={onTest}>
          <Send />
        </Button>
        {!invites && (
          <Button variant="ghost" size="icon-sm" title="Send invites through it" aria-label="Send invites through it" onClick={onInvites}>
            <UserPlus />
          </Button>
        )}
        <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={onEdit}>
          <Pencil />
        </Button>
        <ConfirmDialog
          trigger={
            <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
              <Trash2 />
            </Button>
          }
          title={`Remove ${s.name}?`}
          description={
            invites
              ? "Invites move to another provider, if there is one. Channels sending through it must pick another first."
              : "Channels sending through it must pick another provider first."
          }
          confirmLabel="Remove"
          onConfirm={onRemove}
        />
      </div>
    </Card>
  );
}

function EmailForm({
  service: p,
  editing,
  onBack,
  onDone,
}: {
  service: EmailService;
  /** The provider being edited, possibly switching to another service. */
  editing?: Provider;
  onBack: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  // Editing keeps the saved settings, unless it switches to another service.
  const saved = editing && serviceOf(editing) === p ? editing : null;
  const [form, setForm] = useState<EmailSettings>(() => saved ?? { ...blank, ...p.server, name: editing?.name ?? (p.server ? p.label : "") });
  const save = useMutation({
    meta: { error: "Couldn't save the email provider" },
    mutationFn: () => (editing ? api.updateEmailProvider(editing.id, form) : api.createEmailProvider(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["email"] });
      qc.invalidateQueries({ queryKey: ["notifications"] });
      onDone();
    },
  });
  const set = <K extends keyof EmailSettings>(k: K, v: EmailSettings[K]) => setForm({ ...form, [k]: v });
  // Switching security moves a default port along; without TLS there's no sign-in.
  const setSecurity = (security: EmailSecurity) => {
    const port = securities.some((s) => s.port === form.port) ? securities.find((s) => s.value === security)!.port : form.port;
    setForm({ ...form, security, port, ...(security === "none" && { username: "", password: "" }) });
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const name = (
    <FloatingInput
      label="Name"
      required
      maxLength={64}
      value={form.name}
      onChange={(e) => set("name", e.target.value)}
      placeholder="Alerts"
      description="Shown when picking where invites and notification channels send through."
    />
  );
  const sender = (
    <FloatingInput
      label="Sender"
      required
      autoFocus
      value={form.from}
      onChange={(e) => set("from", e.target.value)}
      placeholder="kipitiny <alerts@example.com>"
      description={p.server ? `On a domain verified in ${p.label}, with an optional name.` : "An address your provider lets you send from, with an optional name."}
    />
  );

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {p.icon}
          {editing ? `Edit ${editing.name}` : p.server ? `Connect ${p.label}` : "Connect an SMTP server"}
        </DialogTitle>
        <DialogDescription>
          {p.help} Saving checks kipitiny can connect and sign in.
        </DialogDescription>
      </DialogHeader>
      {!saved && p.guide && <HowTo label={`How do I set up ${p.label}?`} accent={p.accent} steps={p.guide(senderDomain(form.from))} />}
      {p.server ? (
        <div className="space-y-4">
          {name}
          {sender}
          <FloatingInput
            label={p.secret?.label ?? "Password"}
            type="password"
            autoComplete="new-password"
            required
            value={form.password}
            onChange={(e) => set("password", e.target.value)}
            placeholder={p.secret?.placeholder}
            description={saved ? "Leave as is to keep the saved one." : undefined}
          />
        </div>
      ) : (
        <div className="space-y-4">
          {name}
          <div className="grid gap-4 sm:grid-cols-[1fr_7rem]">
            <FloatingInput label="Host" required value={form.host} onChange={(e) => set("host", e.target.value)} placeholder="smtp.example.com" />
            <FloatingInput
              label="Port"
              type="number"
              min={1}
              max={65535}
              required
              value={String(form.port)}
              onChange={(e) => set("port", Number(e.target.value) || 0)}
            />
          </div>
          <FloatingSelect label="Security" value={form.security} onValueChange={(v) => setSecurity(v as EmailSecurity)} options={securities} />
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
          {sender}
        </div>
      )}
      <DialogFooter>
        <Button type="button" variant="ghost" disabled={save.isPending} onClick={onBack}>
          <ChevronLeft data-icon="inline-start" />
          {editing ? "Change service" : "Back"}
        </Button>
        <Button type="submit" loading={save.isPending}>
          {editing ? "Save" : "Connect"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function TestForm({ provider, onDone }: { provider: Provider; onDone: () => void }) {
  const [to, setTo] = useState("");
  const test = useMutation({
    meta: { error: "The test email wasn't sent" },
    mutationFn: () => api.testEmailProvider(provider.id, to),
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
        <DialogTitle>Send a test through {provider.name}</DialogTitle>
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
