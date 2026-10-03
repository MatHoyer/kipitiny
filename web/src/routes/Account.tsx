import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, ShieldCheck, ShieldOff, TriangleAlert } from "lucide-react";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";
import { encode } from "uqr";
import { CopyButton, CopyField, ErrorText, Section, Tag } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type Account } from "@/api";

/** The signed-in user's own sign-in settings: password and two-factor authentication. */
export function AccountPage() {
  const account = useQuery({ queryKey: ["account"], queryFn: api.account });
  return (
    <>
      <PageHeader crumbs={[{ label: "Account" }]} />
      <PageBody className="max-w-3xl">
        <ErrorText error={account.error} />
        {account.data && (
          <>
            <TwoFactor account={account.data} />
            <ChangePassword username={account.data.username} />
          </>
        )}
      </PageBody>
    </>
  );
}

type Dialogs = "enable" | "codes" | "disable" | null;

function TwoFactor({ account }: { account: Account }) {
  const [open, setOpen] = useState<Dialogs>(null);
  const on = account.totpEnabled;
  return (
    <Section
      title={
        <span className="flex items-center gap-2">
          Two-factor authentication
          {on ? <Tag className="bg-emerald-500/15 text-emerald-700 dark:text-emerald-400">On</Tag> : <Tag>Off</Tag>}
        </span>
      }
      description="Signing in with a password also asks for a code from an authenticator app (1Password, Bitwarden, Google Authenticator…)."
      actions={
        !on && (
          <Button size="sm" onClick={() => setOpen("enable")}>
            <ShieldCheck data-icon="inline-start" />
            Turn on
          </Button>
        )
      }
    >
      {on && (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className={account.recoveryCodes <= 3 ? "text-sm text-amber-600 dark:text-amber-400" : "text-sm text-muted-foreground"}>
            {account.recoveryCodes === 0
              ? "No recovery codes left: generate new ones."
              : `${account.recoveryCodes} recovery code${account.recoveryCodes === 1 ? "" : "s"} left.`}
          </p>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => setOpen("codes")}>
              New recovery codes
            </Button>
            <Button size="sm" variant="outline" className="text-destructive" onClick={() => setOpen("disable")}>
              <ShieldOff data-icon="inline-start" />
              Turn off
            </Button>
          </div>
        </div>
      )}
      <Dialog open={open !== null} onOpenChange={(o) => !o && setOpen(null)}>
        <DialogContent className="sm:max-w-md">
          {/* Remounted on every open, so secrets and codes never show again. */}
          {open === "enable" && <EnableTotp onDone={() => setOpen(null)} />}
          {open === "codes" && <NewRecoveryCodes onDone={() => setOpen(null)} />}
          {open === "disable" && <DisableTotp onDone={() => setOpen(null)} />}
        </DialogContent>
      </Dialog>
    </Section>
  );
}

/** Asks for the account password before a change to how it signs in. */
function ConfirmPassword({
  title,
  description,
  submitLabel,
  pending,
  destructive,
  children,
  onSubmit,
}: {
  title: string;
  description: ReactNode;
  submitLabel: string;
  pending: boolean;
  destructive?: boolean;
  children?: ReactNode;
  onSubmit: (password: string) => void;
}) {
  const [password, setPassword] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSubmit(password);
  };
  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {children}
        <FloatingInput
          label="Your password"
          type="password"
          required
          autoFocus={!children}
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </div>
      <DialogFooter>
        <Button type="submit" variant={destructive ? "destructive" : "default"} loading={pending}>
          {submitLabel}
        </Button>
      </DialogFooter>
    </form>
  );
}

function EnableTotp({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [code, setCode] = useState("");
  const begin = useMutation({ meta: { error: "Couldn't start the setup" }, mutationFn: api.beginTotp });
  const enable = useMutation({
    meta: { error: "Couldn't turn on two-factor authentication" },
    mutationFn: () => api.enableTotp(code),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["account"] }),
    onError: () => setCode(""),
  });

  if (enable.data) return <RecoveryCodes codes={enable.data.recoveryCodes} title="Two-factor authentication is on" onDone={onDone} />;
  if (!begin.data)
    return (
      <ConfirmPassword
        title="Turn on two-factor authentication"
        description="Confirm your password to set up an authenticator app."
        submitLabel="Continue"
        pending={begin.isPending}
        onSubmit={(p) => begin.mutate(p)}
      />
    );
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    enable.mutate();
  };
  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle>Scan with your authenticator app</DialogTitle>
        <DialogDescription>Then enter the 6-digit code it shows.</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <QrCode value={begin.data.uri} />
        <div className="space-y-1.5">
          <p className="text-xs text-muted-foreground">Can't scan it? Enter this key instead:</p>
          <CopyField value={begin.data.secret.replace(/(.{4})/g, "$1 ").trim()} />
        </div>
        <FloatingInput
          label="Code"
          required
          autoFocus
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9 ]{6,7}"
          maxLength={7}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          inputClassName="font-mono tracking-widest"
        />
      </div>
      <DialogFooter>
        <Button type="submit" loading={enable.isPending}>
          Turn on
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Dark modules on white whatever the theme: scanners expect that. */
function QrCode({ value }: { value: string }) {
  const { path, size } = useMemo(() => {
    const qr = encode(value, { ecc: "M", border: 2 });
    let d = "";
    qr.data.forEach((row, y) => row.forEach((on, x) => on && (d += `M${x} ${y}h1v1h-1z`)));
    return { path: d, size: qr.size };
  }, [value]);
  return (
    <svg
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label="QR code for the authenticator app"
      className="mx-auto size-48 rounded-lg bg-white"
      shapeRendering="crispEdges"
    >
      <path d={path} fill="#000" />
    </svg>
  );
}

function RecoveryCodes({ codes, title, onDone }: { codes: string[]; title: string; onDone: () => void }) {
  const text = codes.join("\n");
  const download = () => {
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([`kipitiny recovery codes (${window.location.host})\n\n${text}\n`], { type: "text/plain" }));
    a.download = "kipitiny-recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(a.href);
  };
  return (
    <>
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription className="flex items-start gap-1.5">
          <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-500" />
          Save these recovery codes somewhere safe: each signs you in once if you lose the authenticator app. They won't be shown again.
        </DialogDescription>
      </DialogHeader>
      <ul className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-lg border bg-muted/40 px-4 py-3 font-mono text-sm">
        {codes.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <DialogFooter className="sm:justify-between">
        <div className="flex items-center gap-1">
          <CopyButton value={text} label="Copy all" />
          <Button variant="ghost" size="icon-xs" aria-label="Download" title="Download" onClick={download}>
            <Download />
          </Button>
        </div>
        <Button onClick={onDone}>I saved them</Button>
      </DialogFooter>
    </>
  );
}

function NewRecoveryCodes({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const regenerate = useMutation({
    meta: { error: "Couldn't generate recovery codes" },
    mutationFn: api.regenerateRecoveryCodes,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["account"] }),
  });
  if (regenerate.data) return <RecoveryCodes codes={regenerate.data.recoveryCodes} title="New recovery codes" onDone={onDone} />;
  return (
    <ConfirmPassword
      title="Generate new recovery codes"
      description="Your current recovery codes stop working."
      submitLabel="Generate"
      pending={regenerate.isPending}
      onSubmit={(p) => regenerate.mutate(p)}
    />
  );
}

function DisableTotp({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const disable = useMutation({
    meta: { error: "Couldn't turn off two-factor authentication" },
    mutationFn: api.disableTotp,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["account"] });
      toast.success("Two-factor authentication is off");
      onDone();
    },
  });
  return (
    <ConfirmPassword
      title="Turn off two-factor authentication?"
      description="Your password alone will sign you in. The authenticator app and recovery codes stop working."
      submitLabel="Turn off"
      destructive
      pending={disable.isPending}
      onSubmit={(p) => disable.mutate(p)}
    />
  );
}

function ChangePassword({ username }: { username: string }) {
  const [form, setForm] = useState({ current: "", password: "", confirm: "" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const change = useMutation({
    meta: { error: "Couldn't change the password" },
    mutationFn: () => {
      if (form.password !== form.confirm) throw new Error("Passwords don't match.");
      return api.changePassword(form.current, form.password);
    },
    onSuccess: () => {
      setForm({ current: "", password: "", confirm: "" });
      toast.success("Password changed", { description: "Other sessions were signed out." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    change.mutate();
  };
  return (
    <Section title="Password" description="Changing it signs out every other session.">
      <form onSubmit={onSubmit} className="space-y-4">
        {/* Lets password managers tie the new password to the account. */}
        <input type="text" autoComplete="username" value={username} readOnly hidden />
        <FloatingInput
          label="Current password"
          type="password"
          required
          autoComplete="current-password"
          value={form.current}
          onChange={set("current")}
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <FloatingInput
            label="New password"
            type="password"
            required
            autoComplete="new-password"
            value={form.password}
            onChange={set("password")}
            description="At least 10 characters."
          />
          <FloatingInput
            label="Confirm new password"
            type="password"
            required
            autoComplete="new-password"
            value={form.confirm}
            onChange={set("confirm")}
          />
        </div>
        <div className="flex justify-end">
          <Button type="submit" loading={change.isPending}>
            Change password
          </Button>
        </div>
      </form>
    </Section>
  );
}
