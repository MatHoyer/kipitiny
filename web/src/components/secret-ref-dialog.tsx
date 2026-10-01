import { useQuery } from "@tanstack/react-query";
import { Vault } from "lucide-react";
import { useState, type FormEvent } from "react";
import { ErrorText, Mono } from "@/components/common";
import { Button } from "@/components/ui/button";
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
import { FloatingSelect } from "@/components/ui/floating-select";
import { api } from "../api";

/** An env name for a field: API Keys + token → API_KEYS_TOKEN. */
const entryName = (item: string, field: string) =>
  `${item}_${field.split(".").pop()}`
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/^(\d)/, "_$1");

/**
 * Picks a field of a connected password manager and adds it as a secret
 * referencing it. Hidden when no password manager is connected.
 */
export function SecretRefDialog({ onPick }: { onPick: (key: string, ref: string) => void }) {
  const providers = useQuery({ queryKey: ["secret-providers"], queryFn: api.secretProviders });
  const connected = providers.data?.filter((p) => p.connected) ?? [];
  const [open, setOpen] = useState(false);
  const [provider, setProvider] = useState("");
  const [vault, setVault] = useState("");
  const [item, setItem] = useState("");
  const [ref, setRef] = useState("");
  const [key, setKey] = useState("");

  const providerId = provider || connected[0]?.id || "";
  const vaults = useQuery({
    queryKey: ["secret-vaults", providerId],
    queryFn: () => api.secretVaults(providerId),
    enabled: open && !!providerId,
  });
  const items = useQuery({
    queryKey: ["secret-items", providerId, vault],
    queryFn: () => api.secretItems(providerId, vault),
    enabled: open && !!vault,
  });
  // Titles needn't be unique: items are picked by position.
  const picked = item === "" ? undefined : items.data?.[Number(item)];
  const fields = picked?.fields ?? [];

  if (connected.length === 0) return null;

  const reset = () => {
    setVault("");
    setItem("");
    setRef("");
    setKey("");
  };
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    // Rendered in a portal, but React still bubbles the submit to the env
    // editor's form.
    e.stopPropagation();
    onPick(key.trim(), `{{ ${ref} }}`);
    setOpen(false);
    reset();
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" variant="outline" size="sm">
          <Vault data-icon="inline-start" />
          From password manager
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={onSubmit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>Secret from a password manager</DialogTitle>
            <DialogDescription>
              Fetched on each deploy, never stored by kipitiny. Only vaults the token can read are listed.
            </DialogDescription>
          </DialogHeader>
          {connected.length > 1 && (
            <FloatingSelect
              label="Password manager"
              value={providerId}
              onValueChange={(v) => {
                setProvider(v);
                reset();
              }}
              options={connected.map((p) => ({ value: p.id, label: p.name }))}
            />
          )}
          <FloatingSelect
            label={vaults.isLoading ? "Vault (loading…)" : "Vault"}
            value={vault}
            onValueChange={(v) => {
              setVault(v);
              setItem("");
              setRef("");
            }}
            options={(vaults.data ?? []).map((v) => ({ value: v, label: v }))}
            disabled={!vaults.data?.length}
          />
          <FloatingSelect
            label={items.isLoading ? "Item (loading…)" : "Item"}
            value={item}
            onValueChange={(v) => {
              setItem(v);
              setRef("");
            }}
            options={(items.data ?? []).map((i, n) => ({ value: String(n), label: i.title }))}
            disabled={!items.data?.length}
          />
          <FloatingSelect
            label="Field"
            value={ref}
            onValueChange={(v) => {
              setRef(v);
              const f = fields.find((f) => f.ref === v);
              if (f && picked && !key) setKey(entryName(picked.title, f.name));
            }}
            options={fields.map((f) => ({ value: f.ref, label: f.name }))}
            disabled={fields.length === 0}
            description={ref && <Mono>{ref}</Mono>}
          />
          <FloatingInput label="Name" required value={key} onChange={(e) => setKey(e.target.value)} />
          <ErrorText error={vaults.error ?? items.error} />
          <DialogFooter>
            <Button type="submit" disabled={!ref || !key.trim()}>
              Add secret
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
