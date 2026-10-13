import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, KeyRound, Plus } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { passwordManagerIcon as iconFor } from "@/components/brand-icons";
import { ChoiceTile, EmptyState, ErrorText, Mono, Tag, withCode } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { HowTo } from "@/components/how-to";
import { StepsProgress, TestDialog, type PlannedStep } from "@/components/test-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, type SecretConnection, type SecretProvider, type SecretStep } from "@/api";
import { connectionLabel } from "@/lib/format";
import { SettingsPage } from "./page";

/** The provider's example reference, through the connection with scheme (pass+work://Vault/Item/password). */
const exampleFor = (p: SecretProvider, scheme: string) => scheme + p.example.slice(p.scheme.length);

/** Password managers that service env can reference (e.g. pass://Vault/Item/field), one card per connection. */
export function PasswordManagers() {
  const providers = useQuery({ queryKey: ["secret-providers"], queryFn: api.secretProviders });
  const connections = useQuery({ queryKey: ["secret-connections"], queryFn: api.secretConnections });
  const connected = connections.data ?? [];
  const dialog = <ConnectDialog providers={providers.data ?? []} connections={connected} />;

  return (
    <SettingsPage actions={connected.length > 0 && dialog}>
      <ErrorText error={providers.error ?? connections.error} />
      {providers.data?.length === 0 ? (
        <EmptyState
          icon={KeyRound}
          title="No password manager available"
          description="None ships with this version yet. Use secret entries in the env meanwhile."
        />
      ) : providers.data && connections.data && connected.length === 0 ? (
        <EmptyState
          icon={KeyRound}
          title="No password manager connected"
          description="Reference secrets in a service's env instead of pasting them. kipitiny fetches them on each deploy and never stores them."
          action={dialog}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {connected.map((c) => (
            <ConnectionCard key={c.id} connection={c} provider={providers.data?.find((p) => p.id === c.kind)} />
          ))}
        </div>
      )}
    </SettingsPage>
  );
}

function ConnectionCard({ connection: c, provider: p }: { connection: SecretConnection; provider?: SecretProvider }) {
  const qc = useQueryClient();
  const label = connectionLabel(c);
  const disconnect = useMutation({
    meta: { error: `Couldn't disconnect ${label}` },
    mutationFn: () => api.deleteSecretConnection(c.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secret-connections"] }),
  });

  return (
    <Card className="gap-3 px-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{iconFor(c.kind)}</span>
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{c.name || c.providerName}</p>
          <p className="text-xs text-muted-foreground">{c.name ? c.providerName : "Default connection"}</p>
        </div>
        <Tag className="font-mono">{c.scheme}://</Tag>
      </div>
      {p && (
        <p className="text-xs text-muted-foreground">
          Use in env as <Mono>{`{{ ${exampleFor(p, c.scheme)} }}`}</Mono>
        </p>
      )}
      <div className="mt-auto flex items-center justify-end gap-1">
        <ConnectionTestDialog connection={c} />
        <ConfirmDialog
          trigger={
            <Button variant="outline" size="sm" disabled={disconnect.isPending}>
              Disconnect
            </Button>
          }
          title={`Disconnect ${label}?`}
          description={`Services referencing ${c.scheme}:// can't deploy until it's connected again. Running containers keep their values.`}
          confirmLabel="Disconnect"
          onConfirm={() => disconnect.mutate()}
        />
      </div>
    </Card>
  );
}

/** Pick a provider, then give the connection a name (unless it's the first) and a token. */
function ConnectDialog({ providers, connections }: { providers: SecretProvider[]; connections: SecretConnection[] }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<SecretProvider | null>(null);
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  // Steps the manager reported while connecting, for the providers that show them.
  const [seen, setSeen] = useState<SecretStep[]>([]);
  const stepped = picked?.id === "protonpass";
  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      // Refetched only now: the first connection swaps the empty state, which holds this dialog.
      if (connect.isSuccess) qc.invalidateQueries({ queryKey: ["secret-connections"] });
      setPicked(null);
      setName("");
      setToken("");
      setSeen([]);
      connect.reset();
    }
  };
  const connect = useMutation({
    // The steps show where it failed instead.
    meta: { error: stepped ? false : "Couldn't connect the password manager" },
    mutationFn: () => {
      setSeen([]);
      return api.createSecretConnection(picked!.id, name.trim(), token.trim(), stepped ? (s) => setSeen((prev) => [...prev, s]) : undefined);
    },
    // A stepped connection stays open on its finished steps until closed.
    onSuccess: () => !stepped && onOpenChange(false),
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    connect.mutate();
  };
  // The first connection of a provider is its default, referenced by the plain scheme; the next ones need a name.
  const hasDefault = !!picked && connections.some((c) => c.kind === picked.id && c.name === "");
  const scheme = picked ? (name.trim() ? `${picked.scheme}+${name.trim()}` : picked.scheme) : "";
  const nameInput = picked && (
    <FloatingInput
      label={hasDefault ? "Name" : "Name (optional)"}
      required={hasDefault}
      autoComplete="off"
      spellCheck={false}
      pattern="[a-z][a-z0-9\-]{0,30}"
      title="Lowercase letters, digits and dashes, starting with a letter"
      disabled={connect.isPending || connect.isSuccess}
      value={name}
      onChange={(e) => setName(e.target.value.toLowerCase())}
      inputClassName="font-mono"
      description={
        hasDefault ? (
          <>
            {picked.name} already has a default connection: name this one, like work for a second account. Its references start with{" "}
            <Mono>{scheme}://</Mono>.
          </>
        ) : (
          <>
            Leave empty for the default connection, referenced as <Mono>{picked.scheme}://</Mono>. Name it to connect a second account later.
          </>
        )
      }
    />
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus data-icon="inline-start" />
          Connect password manager
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-xl">
        {!picked ? (
          <>
            <DialogHeader>
              <DialogTitle>Connect a password manager</DialogTitle>
              <DialogDescription>Choose where your secrets live.</DialogDescription>
            </DialogHeader>
            <div className="grid gap-3 sm:grid-cols-2">
              {providers.map((p) => (
                <ChoiceTile
                  key={p.id}
                  icon={iconFor(p.id)}
                  title={p.name}
                  description={p.available ? <Mono>{p.scheme}://</Mono> : "Its CLI isn't installed on the manager."}
                  disabled={!p.available}
                  onClick={() => setPicked(p)}
                />
              ))}
            </div>
          </>
        ) : stepped ? (
          <ProtonPassConnect
            provider={picked}
            nameInput={nameInput}
            scheme={scheme}
            token={token}
            setToken={setToken}
            seen={seen}
            connecting={connect.isPending}
            connected={connect.isSuccess}
            error={connect.error}
            onConnect={() => connect.mutate()}
            onBack={() => setPicked(null)}
            onClose={() => onOpenChange(false)}
          />
        ) : (
          <form onSubmit={onSubmit} className="contents">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
                {iconFor(picked.id)}
                Connect {picked.name}
              </DialogTitle>
              {picked.help && <DialogDescription>{withCode(picked.help)}</DialogDescription>}
            </DialogHeader>
            {nameInput}
            <FloatingInput
              label={picked.tokenLabel}
              type="password"
              required
              autoFocus
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              description={
                <>
                  Then reference secrets as <Mono>{`{{ ${exampleFor(picked, scheme)} }}`}</Mono>.
                </>
              }
            />
            <DialogFooter>
              <Button type="button" variant="ghost" disabled={connect.isPending} onClick={() => setPicked(null)}>
                <ChevronLeft data-icon="inline-start" />
                Back
              </Button>
              <Button type="submit" loading={connect.isPending}>
                Connect
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

const proton = {
  accent: "#6D4AFF",
  login: "pass-cli login",
  create: "pass-cli personal-access-token create --name kipitiny --expiration 1y",
  grant: (vault: string) => `pass-cli personal-access-token access grant --personal-access-token-name kipitiny --vault-name ${vault || "<vault>"}`,
};

/** Proton Pass takes its token; how to get one opens on demand, step by step. */
function ProtonPassConnect({
  provider: p,
  nameInput,
  scheme,
  token,
  setToken,
  seen,
  connecting,
  connected,
  error,
  onConnect,
  onBack,
  onClose,
}: {
  provider: SecretProvider;
  nameInput: ReactNode;
  /** The connection's scheme, for the example reference. */
  scheme: string;
  token: string;
  setToken: (t: string) => void;
  seen: SecretStep[];
  connecting: boolean;
  connected: boolean;
  error: Error | null;
  onConnect: () => void;
  onBack: () => void;
  onClose: () => void;
}) {
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    onConnect();
  };
  const started = connecting || connected || !!error;

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {iconFor(p.id)}
          Connect {p.name}
        </DialogTitle>
        <DialogDescription>kipitiny only sees the vaults the token is granted.</DialogDescription>
      </DialogHeader>
      {nameInput}
      <FloatingInput
        label={p.tokenLabel}
        type="password"
        required
        autoFocus
        autoComplete="off"
        disabled={connecting || connected}
        value={token}
        onChange={(e) => setToken(e.target.value)}
        description={
          <>
            Then reference secrets as <Mono>{`{{ ${exampleFor(p, scheme)} }}`}</Mono>.
          </>
        }
      />
      {!started && <TokenGuide />}
      {started && (
        <div className="grid gap-2.5 rounded-lg border bg-muted/30 p-3 animate-in fade-in slide-in-from-top-1">
          <p className="text-xs font-medium text-muted-foreground">{connected ? "Connected." : connecting ? "Connecting…" : "Connecting failed."}</p>
          <StepsProgress plan={connectPlan(p.name)} seen={seen} running={connecting} done={connected} error={error} accent={proton.accent} />
        </div>
      )}
      <DialogFooter>
        {connected ? (
          <Button type="button" onClick={onClose}>
            Done
          </Button>
        ) : (
          <>
            <Button type="button" variant="ghost" disabled={connecting} onClick={onBack}>
              <ChevronLeft data-icon="inline-start" />
              Back
            </Button>
            <Button type="submit" loading={connecting}>
              {error ? "Try again" : "Connect"}
            </Button>
          </>
        )}
      </DialogFooter>
    </form>
  );
}

/** How to get a Proton Pass token: commands to run on your computer. */
function TokenGuide() {
  const [vault, setVault] = useState("");
  return (
    <HowTo
      label="How do I get a token?"
      accent={proton.accent}
      steps={[
        { title: "Sign in to the Proton Pass CLI", text: "On your computer, with your Proton account. Your password stays there.", copy: proton.login },
        {
          title: "Create a token for kipitiny",
          text: (
            <>
              It prints a token starting with <Mono>pst_</Mono>: paste it above.
            </>
          ),
          copy: proton.create,
        },
        {
          title: "Grant it your vaults",
          text: "Once per vault it may read. Without it, the token sees nothing.",
          extra: <FloatingInput label="Vault name" autoComplete="off" value={vault} onChange={(e) => setVault(e.target.value)} />,
          copy: proton.grant(vault.trim()),
        },
      ]}
    />
  );
}

/** What each step the manager reports does, said for the user. */
const stages = (name: string): Record<SecretStep, Omit<PlannedStep, "id">> => ({
  image: { title: `Download the ${name} CLI`, hint: "About 60MB, the first time only", skipped: "Already downloaded" },
  helper: { title: "Start its container", hint: "It runs only while used and is removed after 5 idle minutes", skipped: "Already running" },
  login: { title: "Sign in with the token", hint: "Passed on stdin: it never shows in the container's config" },
  verify: { title: "Check the account", hint: `Asks ${name} who the token belongs to` },
  session: { title: "Check the saved session", hint: "Lists your vaults with it, as a deploy would" },
  vaults: { title: "List the vaults again", hint: "With the new session" },
});

const planOf = (name: string, ids: SecretStep[]): PlannedStep[] => ids.map((id) => ({ id, ...stages(name)[id] }));

/** Connecting: get the CLI running, then sign in. */
const connectPlan = (name: string) => planOf(name, ["image", "helper", "login", "verify"]);

/** Testing: check the saved session; when it expired, sign in again with the saved token. */
const testPlan = (name: string) => (seen: string[]) => {
  if (!seen.includes("login")) return planOf(name, ["image", "helper", "session"]);
  return planOf(name, ["image", "helper", "session", "login", "verify", "vaults"]).map((s) =>
    s.id === "session" ? { ...s, hint: "It had expired" } : s.id === "login" ? { ...s, title: "Sign in again with the saved token" } : s,
  );
};

/** Tests a connection, step by step, as a deploy would run them. */
function ConnectionTestDialog({ connection: c }: { connection: SecretConnection }) {
  return (
    <TestDialog
      name={connectionLabel(c)}
      icon={iconFor(c.kind)}
      description="Checks the manager can still read your vaults, as a deploy would."
      accent={c.kind === "protonpass" ? proton.accent : undefined}
      plan={testPlan(c.providerName)}
      run={(onStep) => api.testSecretConnection(c.id, onStep)}
    />
  );
}
