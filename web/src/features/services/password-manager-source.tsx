import { useQuery } from "@tanstack/react-query";
import { FolderSearch, KeyRound, RefreshCw } from "lucide-react";
import { useRef, useState } from "react";
import { Link } from "react-router";
import { passwordManagerIcon } from "@/components/brand-icons";
import { ErrorText } from "@/components/common";
import { Button } from "@kipit-labs/kipitidy/button";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { FloatingSelect } from "@kipit-labs/kipitidy/floating-select";
import { connectionLabel } from "@/lib/format";
import { api } from "../../api";

/** The password managers a reference can come from, and the one picked. */
export type ManagerSource = ReturnType<typeof useManagerSource>;

/**
 * The password manager connections on offer, plus scheme's own when its
 * connection doesn't exist (anymore), with the one picked (the first by
 * default). scheme is the edited reference's, or "".
 */
export function useManagerSource(scheme: string, enabled: boolean) {
  const connections = useQuery({ queryKey: ["secret-connections"], queryFn: api.secretConnections, enabled });
  const connected = connections.data ?? [];
  const sources = [
    ...connected.map((c) => ({ value: c.scheme, label: connectionLabel(c), icon: passwordManagerIcon(c.kind) })),
    ...(scheme && !connected.some((c) => c.scheme === scheme)
      ? [{ value: scheme, label: `${scheme}://`, icon: <KeyRound /> }]
      : []),
  ];
  const [picked, setSource] = useState(scheme);
  const source = picked || sources[0]?.value || "";
  return {
    connections,
    sources,
    source,
    setSource,
    /** Unset when the source's connection doesn't exist: no browsing. */
    connectionId: connected.find((c) => c.scheme === source)?.id,
    /** Whether any password manager is connected. */
    any: connected.length > 0,
  };
}

/** Picks a password manager reference: the manager, then its path, typed or browsed. */
export function ManagerSourceFields({
  ms,
  path,
  onPathChange,
  onPickField,
}: {
  ms: ManagerSource;
  path: string;
  onPathChange: (path: string) => void;
  onPickField?: (item: string, field: string) => void;
}) {
  return (
    <>
      {ms.connections.isSuccess && ms.sources.length === 0 && (
        <p className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">
          No password manager is connected.{" "}
          <Link to="/password-managers" className="font-medium text-foreground underline-offset-4 hover:underline">
            Connect one in Settings
          </Link>
          .
        </p>
      )}
      {ms.sources.length > 0 && (
        <FloatingSelect
          label="Password manager"
          value={ms.source}
          onValueChange={ms.setSource}
          loading={ms.connections.isLoading}
          options={ms.sources.map((s) => ({
            value: s.value,
            label: (
              <span className="flex items-center gap-2 [&>svg]:size-4 [&>svg]:shrink-0">
                {s.icon}
                {s.label}
              </span>
            ),
          }))}
        />
      )}
      {ms.source && (
        <ProviderPath
          scheme={ms.source}
          connectionId={ms.connectionId}
          path={path}
          onChange={onPathChange}
          onPickField={onPickField ?? (() => {})}
        />
      )}
    </>
  );
}

/**
 * The path of a password manager reference, typed after its scheme or picked
 * by browsing the provider's vaults.
 */
function ProviderPath({
  scheme,
  connectionId,
  path,
  onChange,
  onPickField,
}: {
  scheme: string;
  /** Unset when the scheme's connection doesn't exist: no browsing. */
  connectionId?: string;
  path: string;
  onChange: (path: string) => void;
  onPickField: (item: string, field: string) => void;
}) {
  const [browsing, setBrowsing] = useState(false);
  const [vault, setVault] = useState("");
  const [item, setItem] = useState("");
  // Set while Refresh refetches past the manager's cache.
  const fresh = useRef(false);
  const vaults = useQuery({
    queryKey: ["secret-vaults", connectionId],
    queryFn: () => api.secretVaults(connectionId!, fresh.current),
    enabled: browsing && !!connectionId,
  });
  const items = useQuery({
    queryKey: ["secret-items", connectionId, vault],
    queryFn: () => api.secretItems(connectionId!, vault, fresh.current),
    enabled: browsing && !!connectionId && !!vault,
  });
  const refresh = async () => {
    fresh.current = true;
    try {
      await Promise.all([vaults.refetch(), vault ? items.refetch() : undefined]);
    } finally {
      fresh.current = false;
    }
  };
  // Titles needn't be unique: items are picked by position.
  const picked = item === "" ? undefined : items.data?.[Number(item)];
  const prefix = `${scheme}://`;
  const fieldRef = `${prefix}${path}`;

  return (
    <div className="space-y-3">
      <div className="flex items-start gap-2">
        <span className="flex h-14 shrink-0 items-center rounded-xl border bg-muted px-3 font-mono text-sm text-muted-foreground">
          {prefix}
        </span>
        <FloatingInput
          label="Reference"
          required
          value={path}
          // A pasted full reference drops its scheme.
          onChange={(e) => onChange(e.target.value.startsWith(prefix) ? e.target.value.slice(prefix.length) : e.target.value)}
          placeholder="Vault/Item/field"
          spellCheck={false}
          className="min-w-0 flex-1"
          inputClassName="font-mono text-sm"
        />
        {connectionId && (
          <Button
            type="button"
            variant={browsing ? "secondary" : "outline"}
            size="icon"
            className="mt-3 shrink-0"
            title="Browse"
            aria-label="Browse"
            aria-pressed={browsing}
            onClick={() => setBrowsing(!browsing)}
          >
            <FolderSearch />
          </Button>
        )}
      </div>
      {browsing && connectionId && (
        <div className="space-y-3 rounded-xl border border-dashed p-3">
          <FloatingSelect
            label="Vault"
            loading={vaults.isLoading}
            value={vault}
            onValueChange={(v) => {
              setVault(v);
              setItem("");
            }}
            options={(vaults.data ?? []).map((v) => ({ value: v, label: v }))}
            disabled={!vaults.data?.length}
            description="Only vaults the token can read are listed."
          />
          <FloatingSelect
            label="Item"
            loading={items.isLoading}
            value={item}
            onValueChange={setItem}
            options={(items.data ?? []).map((i, n) => ({ value: String(n), label: i.title }))}
            disabled={!items.data?.length}
          />
          <FloatingSelect
            label="Field"
            value={picked?.fields.some((f) => f.ref === fieldRef) ? fieldRef : ""}
            onValueChange={(ref) => {
              const f = picked?.fields.find((f) => f.ref === ref);
              if (!f || !picked) return;
              onChange(ref.startsWith(prefix) ? ref.slice(prefix.length) : ref);
              onPickField(picked.title, f.name);
            }}
            options={(picked?.fields ?? []).map((f) => ({ value: f.ref, label: f.name }))}
            disabled={!picked?.fields.length}
          />
          <div className="flex items-center justify-between gap-2">
            <ErrorText error={vaults.error ?? items.error} />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="ml-auto"
              loading={vaults.isFetching || items.isFetching}
              onClick={refresh}
            >
              <RefreshCw data-icon="inline-start" />
              Refresh
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
