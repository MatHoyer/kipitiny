import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, LogIn, Pencil, Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { AuthentikIcon, GoogleIcon, KeycloakIcon, MicrosoftIcon } from "@/components/brand-icons";
import { ChoiceTile, CopyField, EmptyState, ErrorText, Mono } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { HowTo, type HowToStep } from "@/components/how-to";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { api, ssoCallbackUrl, type SSOProvider, type SSOProviderInput } from "@/api";
import { SettingsPage } from "./page";

/** A well-known identity provider: where its issuer is and how to register kipitiny there. "other" is any OpenID provider. */
type Preset = {
  id: string;
  label: string;
  icon: ReactNode;
  description: string;
  /** The issuer, when it is the same for everyone. */
  issuer?: string;
  issuerPlaceholder: string;
  matches: (issuer: string) => boolean;
  guide: (callback: string) => HowToStep[];
};

const presets: Preset[] = [
  {
    id: "google",
    label: "Google",
    icon: <GoogleIcon />,
    description: "Google accounts and Google Workspace.",
    issuer: "https://accounts.google.com",
    issuerPlaceholder: "https://accounts.google.com",
    matches: (i) => i.startsWith("https://accounts.google.com"),
    guide: (callback) => [
      {
        title: "Create an OAuth client",
        text: "In Google Cloud › APIs & Services › Credentials: Create credentials › OAuth client ID, type Web application.",
        link: { href: "https://console.cloud.google.com/apis/credentials", label: "Open credentials" },
      },
      { title: "Add the redirect URI", text: "Under Authorized redirect URIs.", copy: callback },
      { title: "Copy the client ID and secret", text: "Google shows them once the client is created." },
    ],
  },
  {
    id: "microsoft",
    label: "Microsoft Entra ID",
    icon: <MicrosoftIcon />,
    description: "Microsoft 365 and Azure accounts of your organization.",
    issuerPlaceholder: "https://login.microsoftonline.com/<tenant ID>/v2.0",
    matches: (i) => i.startsWith("https://login.microsoftonline.com/"),
    guide: (callback) => [
      {
        title: "Register an application",
        text: "In Entra ID › App registrations › New registration, with a Web redirect URI:",
        link: { href: "https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade", label: "Open app registrations" },
        copy: callback,
      },
      {
        title: "Note the tenant",
        text: (
          <>
            The issuer is <Mono>https://login.microsoftonline.com/&lt;Directory (tenant) ID&gt;/v2.0</Mono>; the client ID is the Application
            (client) ID.
          </>
        ),
      },
      { title: "Create a client secret", text: "Certificates & secrets › New client secret. Copy its value, not its ID." },
    ],
  },
  {
    id: "authentik",
    label: "Authentik",
    icon: <AuthentikIcon className="text-[#FD4B2D]" />,
    description: "A self-hosted authentik instance.",
    issuerPlaceholder: "https://auth.example.com/application/o/kipitiny/",
    matches: (i) => i.includes("/application/o/"),
    guide: (callback) => [
      {
        title: "Create a provider and an application",
        text: "Applications › Create with provider, type OAuth2/OpenID Provider, client type Confidential, with this redirect URI:",
        copy: callback,
      },
      { title: "Copy the issuer", text: "The provider's OpenID Configuration Issuer, ending with the application's slug and a slash." },
      { title: "Copy the client ID and secret", text: "From the provider's page." },
    ],
  },
  {
    id: "keycloak",
    label: "Keycloak",
    icon: <KeycloakIcon />,
    description: "A realm of a Keycloak server.",
    issuerPlaceholder: "https://keycloak.example.com/realms/company",
    matches: (i) => i.includes("/realms/"),
    guide: (callback) => [
      { title: "Create a client", text: "In the realm: Clients › Create client, OpenID Connect, with Client authentication on." },
      { title: "Add the redirect URI", text: "Under Valid redirect URIs.", copy: callback },
      { title: "Copy the secret", text: "From the client's Credentials tab. The issuer is the realm's URL." },
    ],
  },
  {
    id: "other",
    label: "Other",
    icon: <LogIn />,
    description: "Any OpenID Connect provider: Pocket ID, Zitadel, Okta, Auth0…",
    issuerPlaceholder: "https://id.example.com",
    matches: () => true,
    guide: (callback) => [
      { title: "Register kipitiny as a client", text: "A confidential web client using the authorization code flow, with this redirect URI:", copy: callback },
      { title: "Copy the issuer", text: <>The URL its discovery document is under, without <Mono>/.well-known/openid-configuration</Mono>.</> },
    ],
  },
];

const presetFor = (issuer: string) => presets.find((p) => p.matches(issuer))!;

type Editing = { step: "pick" } | { step: "new"; preset: Preset } | { step: "edit"; provider: SSOProvider };

/** OpenID Connect providers users sign in with, once an identity is linked to their account. */
export function SSOPage() {
  const qc = useQueryClient();
  const providers = useQuery({ queryKey: ["sso-providers"], queryFn: api.ssoProviders });
  const [editing, setEditing] = useState<Editing | null>(null);
  const remove = useMutation({
    meta: { error: "Couldn't remove the provider" },
    mutationFn: api.deleteSSOProvider,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sso-providers"] }),
  });
  const add = (
    <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
      <Plus data-icon="inline-start" />
      Add provider
    </Button>
  );

  return (
    <SettingsPage actions={!!providers.data?.length && add}>
      <ErrorText error={providers.error} />
      {providers.data?.length === 0 ? (
        <EmptyState
          icon={LogIn}
          title="No single sign-on"
          description="Add an OpenID Connect provider (Google, Microsoft, Authentik, Keycloak…) to sign in with it. Invites and Account link identities to users: the provider never creates accounts."
          action={add}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {providers.data?.map((p) => {
            const preset = presetFor(p.issuer);
            return (
              <Card key={p.id} className="gap-3 px-4">
                <div className="flex items-start gap-3">
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">{preset.icon}</span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium">{p.name}</p>
                    <p className="truncate text-xs text-muted-foreground">{p.issuer}</p>
                  </div>
                </div>
                <div className="mt-auto flex justify-end gap-0.5">
                  <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={() => setEditing({ step: "edit", provider: p })}>
                    <Pencil />
                  </Button>
                  <ConfirmDialog
                    trigger={
                      <Button variant="ghost" size="icon-sm" title="Remove" aria-label="Remove" className="text-muted-foreground hover:text-destructive">
                        <Trash2 />
                      </Button>
                    }
                    title={`Remove ${p.name}?`}
                    description="Identities linked through it are unlinked: their users sign in with a password or a passkey instead. Make sure each has one."
                    confirmLabel="Remove"
                    onConfirm={() => remove.mutate(p.id)}
                  />
                </div>
              </Card>
            );
          })}
        </div>
      )}
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          {editing?.step === "pick" && (
            <>
              <DialogHeader>
                <DialogTitle>Add a provider</DialogTitle>
                <DialogDescription>Where do your users sign in?</DialogDescription>
              </DialogHeader>
              <div className="grid gap-3 sm:grid-cols-2">
                {presets.map((p) => (
                  <ChoiceTile key={p.id} icon={p.icon} title={p.label} description={p.description} onClick={() => setEditing({ step: "new", preset: p })} />
                ))}
              </div>
            </>
          )}
          {editing?.step === "new" && (
            <ProviderForm key={editing.preset.id} preset={editing.preset} provider={null} onBack={() => setEditing({ step: "pick" })} onDone={() => setEditing(null)} />
          )}
          {editing?.step === "edit" && (
            <ProviderForm key={editing.provider.id} preset={presetFor(editing.provider.issuer)} provider={editing.provider} onDone={() => setEditing(null)} />
          )}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function ProviderForm({
  preset: p,
  provider,
  onBack,
  onDone,
}: {
  preset: Preset;
  provider: SSOProvider | null;
  onBack?: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<SSOProviderInput>(
    provider
      ? { name: provider.name, issuer: provider.issuer, clientId: provider.clientId, clientSecret: provider.clientSecret }
      : { name: p.id === "other" ? "" : p.label, issuer: p.issuer ?? "", clientId: "", clientSecret: "" },
  );
  const set = (k: keyof SSOProviderInput) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = useMutation({
    meta: { error: "Couldn't save the provider" },
    mutationFn: () => (provider ? api.updateSSOProvider(provider.id, form) : api.createSSOProvider(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sso-providers"] });
      onDone();
    },
  });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  const callback = ssoCallbackUrl();

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {p.icon}
          {provider ? `Edit ${provider.name}` : `Connect ${p.label}`}
        </DialogTitle>
        <DialogDescription>Register kipitiny as an OpenID Connect client there. kipitiny reads the issuer's configuration before saving.</DialogDescription>
      </DialogHeader>
      {!provider && <HowTo label={`How do I set up ${p.label}?`} steps={p.guide(callback)} />}
      <div className="space-y-2">
        <p className="text-sm font-medium">Redirect URI</p>
        <CopyField value={callback} />
      </div>
      <div className="space-y-4">
        <FloatingInput label="Name" required autoFocus value={form.name} onChange={set("name")} description="On the sign-in button: Sign in with …" />
        {!p.issuer && (
          <FloatingInput label="Issuer URL" required type="url" value={form.issuer} onChange={set("issuer")} placeholder={p.issuerPlaceholder} />
        )}
        <FloatingInput label="Client ID" required value={form.clientId} onChange={set("clientId")} autoComplete="off" />
        <FloatingInput
          label="Client secret"
          required
          type="password"
          value={form.clientSecret}
          onChange={set("clientSecret")}
          autoComplete="new-password"
          description={provider ? "Leave as is to keep the saved one." : undefined}
        />
      </div>
      <DialogFooter>
        {onBack && (
          <Button type="button" variant="ghost" disabled={save.isPending} onClick={onBack}>
            <ChevronLeft data-icon="inline-start" />
            Back
          </Button>
        )}
        <Button type="submit" loading={save.isPending}>
          {provider ? "Save" : "Connect"}
        </Button>
      </DialogFooter>
    </form>
  );
}
