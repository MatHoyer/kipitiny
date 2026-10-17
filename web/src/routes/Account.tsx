import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing, Download, Fingerprint, KeyRound, Link2, LogIn, Plus, ShieldCheck, ShieldOff, Smartphone, Trash2, TriangleAlert } from "lucide-react";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { encode } from "uqr";
import { CopyButton, CopyField, EmptyState, ErrorText, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ConfirmField, useConfirmation } from "@/components/confirm-identity";
import { PageBody, PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { friendlyError } from "@/lib/errors";
import { timeAgo } from "@/lib/format";
import { currentPushSubscription, enablePush, pushNeedsInstall, pushSupported } from "@/lib/push";
import { leaveFor } from "@/lib/sso";
import { isCancelled, passkeysSupported } from "@/lib/webauthn";
import { api, type Account, type SSOButton } from "@/api";

/** The signed-in user's own settings: password, two-factor authentication, passkeys, push notifications. */
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
            <Passkeys account={account.data} />
            <SingleSignOn account={account.data} />
            <ChangePassword account={account.data} />
            {account.data.role === "admin" && <PushNotifications />}
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

/** Asks for the account password (or a recent sign-in, without one) before a change to how it signs in. */
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
  const confirm = useConfirmation();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSubmit(confirm.password);
  };
  return (
    <form onSubmit={submit} className="contents">
      <DialogHeader>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {children}
        <ConfirmField confirm={confirm} autoFocus={!children} />
      </div>
      <DialogFooter>
        <Button type="submit" variant={destructive ? "destructive" : "default"} loading={pending} disabled={!confirm.ready}>
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

function Passkeys({ account }: { account: Account }) {
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const supported = passkeysSupported();
  const remove = useMutation({
    meta: { error: "Couldn't remove the passkey" },
    mutationFn: api.deletePasskey,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["account"] }),
  });
  const add = (
    <Button size="sm" onClick={() => setAdding(true)} disabled={!supported}>
      <Plus data-icon="inline-start" />
      Add passkey
    </Button>
  );
  return (
    <Section
      title="Passkeys"
      description="Sign in with your fingerprint, face or device PIN instead of a password. A passkey stands in for two-factor authentication too."
      actions={account.passkeys.length > 0 && add}
    >
      {account.ssoRequired && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          This server requires single sign-on: passkeys don't sign in for now. They work again if an admin stops requiring it.
        </p>
      )}
      {!supported && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          Passkeys need the manager to be opened over HTTPS at a domain name (or at localhost), in a browser that supports them.
        </p>
      )}
      {account.passkeys.length === 0 ? (
        <EmptyState
          icon={Fingerprint}
          title="No passkeys yet"
          description={`A passkey only works at ${window.location.hostname}, where it was added.`}
          action={add}
        />
      ) : (
        <ul className="divide-y rounded-lg border">
          {account.passkeys.map((p) => (
            <li key={p.id} className="flex items-center gap-3 px-4 py-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-4">
                <KeyRound />
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{p.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  Added {timeAgo(p.createdAt)} · {p.lastUsedAt ? `last used ${timeAgo(p.lastUsedAt)}` : "never used"}
                </p>
              </div>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                    <Trash2 />
                  </Button>
                }
                title={`Remove passkey ${p.name}?`}
                description="It won't sign you in anymore. Also delete it from the device or password manager holding it."
                confirmLabel="Remove"
                onConfirm={() => remove.mutate(p.id)}
              />
            </li>
          ))}
        </ul>
      )}
      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="sm:max-w-md">{adding && <AddPasskey onDone={() => setAdding(false)} />}</DialogContent>
      </Dialog>
    </Section>
  );
}

/** The admin's browsers and phones that get the push channels' notifications. */
function PushNotifications() {
  const qc = useQueryClient();
  const supported = pushSupported();
  const devices = useQuery({ queryKey: ["push"], queryFn: api.pushDevices });
  const current = useQuery({ queryKey: ["push", "current"], queryFn: currentPushSubscription, enabled: supported });
  const notifications = useQuery({ queryKey: ["notifications"], queryFn: api.notifications });
  const refresh = () => qc.invalidateQueries({ queryKey: ["push"] });
  const list = devices.data?.devices ?? [];
  const here = list.find((d) => d.endpoint === current.data?.endpoint);
  const turnOn = useMutation({
    meta: { error: "Couldn't turn push notifications on" },
    mutationFn: () => enablePush(devices.data!.publicKey),
    onSuccess: refresh,
  });
  const remove = useMutation({
    meta: { error: "Couldn't remove the device" },
    mutationFn: async (id: string) => {
      if (id === here?.id) await current.data?.unsubscribe();
      await api.unsubscribePush(id);
    },
    onSuccess: refresh,
  });
  const noChannel = notifications.data && !notifications.data.channels.some((c) => c.kind === "push" && c.enabled);
  return (
    <Section
      title="Push notifications"
      description="Get alerts on this device like an app's, even with the manager closed."
      actions={
        supported &&
        !here && (
          <Button size="sm" onClick={() => turnOn.mutate()} disabled={!devices.data || turnOn.isPending}>
            <BellRing data-icon="inline-start" />
            Turn on for this device
          </Button>
        )
      }
    >
      <ErrorText error={devices.error} />
      {!supported && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          {pushNeedsInstall()
            ? "On iPhone and iPad, add the manager to the home screen (Share › Add to Home Screen), open it from there, then turn push on."
            : "Push notifications need the manager opened over HTTPS (or at localhost), in a browser that supports them."}
        </p>
      )}
      {noChannel && (
        <p className="flex items-center gap-1.5 text-sm text-amber-600 dark:text-amber-500">
          <TriangleAlert className="size-4 shrink-0" />
          <span>
            No push channel yet: add one in{" "}
            <Link to="/notifications" className="underline">
              Settings › Notifications
            </Link>{" "}
            to pick the events sent.
          </span>
        </p>
      )}
      {list.length > 0 && (
        <ul className="divide-y rounded-lg border">
          {list.map((d) => (
            <li key={d.id} className="flex items-center gap-3 px-4 py-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-4">
                <Smartphone />
              </span>
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 truncate text-sm font-medium">
                  {d.name}
                  {d.id === here?.id && <Tag>This device</Tag>}
                </p>
                <p className="truncate text-xs text-muted-foreground">Added {timeAgo(d.createdAt)}</p>
              </div>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                    <Trash2 />
                  </Button>
                }
                title={`Stop push notifications on ${d.name}?`}
                description="It won't get notifications anymore. Turn them on again from that device."
                confirmLabel="Remove"
                onConfirm={() => remove.mutate(d.id)}
              />
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

/** Identities at single sign-on providers that sign in to this account. */
function SingleSignOn({ account }: { account: Account }) {
  const qc = useQueryClient();
  const [linking, setLinking] = useState<SSOButton | null>(null);
  const unlink = useMutation({
    meta: { error: "Couldn't unlink the account" },
    mutationFn: api.unlinkSSO,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["account"] }),
  });
  const unlinked = account.ssoProviders.filter((p) => !account.sso.some((i) => i.providerId === p.id));
  if (account.ssoProviders.length === 0 && account.sso.length === 0) return null;
  const links = unlinked.length > 0 && (
    <div className="flex flex-wrap gap-2">
      {unlinked.map((p) => (
        <Button key={p.id} size="sm" variant={account.sso.length ? "outline" : "default"} onClick={() => setLinking(p)}>
          <Link2 data-icon="inline-start" />
          Link {p.name}
        </Button>
      ))}
    </div>
  );
  return (
    <Section
      title="Single sign-on"
      description="Sign in with your account at an identity provider. Two-factor authentication still applies when it's on."
      actions={account.sso.length > 0 && links}
    >
      {account.sso.length === 0 ? (
        <EmptyState icon={LogIn} title="No linked accounts" description="Link one to see a Sign in with … button on the sign-in page." action={links} />
      ) : (
        <ul className="divide-y rounded-lg border">
          {account.sso.map((i) => (
            <li key={i.id} className="flex items-center gap-3 px-4 py-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-4">
                <LogIn />
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {i.providerName}
                  {i.email && <span className="font-normal text-muted-foreground"> · {i.email}</span>}
                </p>
                <p className="truncate text-xs text-muted-foreground">
                  Linked {timeAgo(i.createdAt)} · {i.lastUsedAt ? `last used ${timeAgo(i.lastUsedAt)}` : "never used"}
                </p>
              </div>
              <ConfirmDialog
                trigger={
                  <Button variant="ghost" size="icon-sm" title="Unlink" aria-label="Unlink" className="text-muted-foreground hover:text-destructive">
                    <Trash2 />
                  </Button>
                }
                title={`Unlink ${i.providerName}?`}
                description="It won't sign you in anymore. You can link it again later."
                confirmLabel="Unlink"
                onConfirm={() => unlink.mutate(i.id)}
              />
            </li>
          ))}
        </ul>
      )}
      <Dialog open={linking !== null} onOpenChange={(o) => !o && setLinking(null)}>
        <DialogContent className="sm:max-w-md">{linking && <LinkSSO provider={linking} />}</DialogContent>
      </Dialog>
    </Section>
  );
}

function LinkSSO({ provider }: { provider: SSOButton }) {
  const link = useMutation({
    meta: { error: "Couldn't link the account" },
    mutationFn: async (password: string) => leaveFor((await api.connectSSO(provider.id, password)).url),
  });
  return (
    <ConfirmPassword
      title={`Link ${provider.name}`}
      description={`Confirm your password, then sign in at ${provider.name}: the account you use there will sign in here.`}
      submitLabel="Continue"
      pending={link.isPending}
      onSubmit={(p) => link.mutate(p)}
    />
  );
}

function AddPasskey({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const add = useMutation({
    meta: { error: false },
    mutationFn: (password: string) => api.addPasskey(password, name.trim()),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["account"] });
      toast.success(`Passkey ${p.name} added`);
      onDone();
    },
    onError: (err) => !isCancelled(err) && toast.error("Couldn't add the passkey", { description: friendlyError(err), duration: 8000 }),
  });
  return (
    <ConfirmPassword
      title="Add a passkey"
      description="Confirm your password, then follow your browser's prompt."
      submitLabel="Continue"
      pending={add.isPending}
      onSubmit={(p) => add.mutate(p)}
    >
      <FloatingInput
        label="Name"
        required
        autoFocus
        maxLength={60}
        placeholder="MacBook Touch ID"
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
    </ConfirmPassword>
  );
}

function ChangePassword({ account }: { account: Account }) {
  const qc = useQueryClient();
  const [form, setForm] = useState({ current: "", password: "", confirm: "" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const confirm = useConfirmation();
  const has = account.hasPassword;
  const change = useMutation({
    meta: { error: has ? "Couldn't change the password" : "Couldn't set the password" },
    mutationFn: () => {
      if (form.password !== form.confirm) throw new Error("Passwords don't match.");
      return api.changePassword(form.current, form.password);
    },
    onSuccess: () => {
      setForm({ current: "", password: "", confirm: "" });
      qc.invalidateQueries({ queryKey: ["account"] });
      toast.success(has ? "Password changed" : "Password set", { description: "Other sessions were signed out." });
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    change.mutate();
  };
  return (
    <Section
      title="Password"
      description={
        has ? "Changing it signs out every other session." : "Your account signs in with single sign-on only. A password lets it sign in without the provider too."
      }
    >
      <form onSubmit={onSubmit} className="space-y-4">
        {/* Lets password managers tie the new password to the account. */}
        <input type="text" autoComplete="username" value={account.username} readOnly hidden />
        {has ? (
          <FloatingInput
            label="Current password"
            type="password"
            required
            autoComplete="current-password"
            value={form.current}
            onChange={set("current")}
          />
        ) : (
          <ConfirmField confirm={confirm} />
        )}
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
          <Button type="submit" loading={change.isPending} disabled={!has && !confirm.ready}>
            {has ? "Change password" : "Set password"}
          </Button>
        </div>
      </form>
    </Section>
  );
}
