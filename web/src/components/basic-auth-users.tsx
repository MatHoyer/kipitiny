import { Eye, EyeOff, KeyRound, Pencil, Plus, Trash2, UserRound, Vault } from "lucide-react";
import { useState, type FormEvent } from "react";
import { ManagerSourceFields, useManagerSource } from "@/components/env-entry-dialog";
import { Empty } from "@/components/common";
import { ManagerRefValue } from "@/components/template-value";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { managerRef } from "@/lib/format";

/**
 * A basic auth user as edited. password is a new password, a password
 * manager reference ({{ scheme://… }}), or empty to keep the stored one.
 */
export type BasicAuthRow = { name: string; password: string; stored: boolean };

const userRe = /^[A-Za-z0-9._@-]{1,64}$/;
const minPassword = 10;

/** Basic auth users, each with a password typed here or kept in a password manager. */
export function BasicAuthUsers({ rows, onChange }: { rows: BasicAuthRow[]; onChange: (rows: BasicAuthRow[]) => void }) {
  // Adds (index null) or edits the user at index; n remounts it fresh.
  const [dialog, setDialog] = useState<{ open: boolean; index: number | null; n: number }>({ open: false, index: null, n: 0 });
  const openDialog = (index: number | null) => setDialog((d) => ({ open: true, index, n: d.n + 1 }));
  const editing = dialog.index != null ? (rows[dialog.index] ?? null) : null;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">Basic auth</h3>
          <p className="text-sm text-muted-foreground">Visitors log in as one of these users.</p>
        </div>
        <Button type="button" size="sm" variant="outline" onClick={() => openDialog(null)}>
          <Plus data-icon="inline-start" />
          Add user
        </Button>
      </div>
      {rows.length === 0 ? (
        <Empty>No basic auth: the site is open to anyone who can reach it.</Empty>
      ) : (
        <ul className="space-y-1.5">
          {rows.map((u, i) => {
            const mref = managerRef(u.password);
            return (
              <li key={i}>
                <div
                  className="flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 py-1.5 transition-colors hover:bg-muted/50"
                  onClick={() => openDialog(i)}
                >
                  <span className="w-2/5 shrink-0 truncate font-mono text-sm">{u.name}</span>
                  <span className="flex min-w-0 flex-1 items-center">
                    {mref ? (
                      <ManagerRefValue scheme={mref.scheme} path={mref.path} />
                    ) : (
                      <span className="font-mono text-xs tracking-widest text-muted-foreground" title={u.password ? "New password" : "Saved password"}>
                        ••••••••
                      </span>
                    )}
                  </span>
                  <span className="flex shrink-0 gap-0.5">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      title="Edit"
                      aria-label={`Edit ${u.name}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        openDialog(i);
                      }}
                    >
                      <Pencil />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      title="Remove"
                      aria-label={`Remove ${u.name}`}
                      className="text-muted-foreground hover:text-destructive"
                      onClick={(e) => {
                        e.stopPropagation();
                        onChange(rows.filter((_, j) => j !== i));
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      <Dialog open={dialog.open} onOpenChange={(open) => setDialog((d) => ({ ...d, open }))}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
          <UserForm
            key={dialog.n}
            user={editing}
            taken={rows.filter((_, j) => j !== dialog.index).map((r) => r.name)}
            onDone={(row) => {
              onChange(dialog.index != null ? rows.map((r, j) => (j === dialog.index ? row : r)) : [...rows, row]);
              setDialog((d) => ({ ...d, open: false }));
            }}
          />
        </DialogContent>
      </Dialog>
    </div>
  );
}

function UserForm({ user, taken, onDone }: { user: BasicAuthRow | null; taken: string[]; onDone: (row: BasicAuthRow) => void }) {
  const initialRef = user ? managerRef(user.password) : null;
  const [fromManager, setFromManager] = useState(!!initialRef);
  const ms = useManagerSource(initialRef?.scheme ?? "", true);
  const [name, setName] = useState(user?.name ?? "");
  const [password, setPassword] = useState(user && !initialRef ? user.password : "");
  const [path, setPath] = useState(initialRef?.path.join("/") ?? "");
  const [shown, setShown] = useState(false);

  const key = name.trim();
  const badName = !!key && !userRe.test(key);
  const dupName = taken.includes(key);
  // A stored password (not a reference) is kept when left empty, under the
  // same name: the manager finds it by name.
  const savedPassword = !!user?.stored && !initialRef;
  const renamed = savedPassword && key !== user.name;
  const keeps = savedPassword && !renamed && !fromManager && !password;
  const shortPassword = !fromManager && !!password && password.length < minPassword;
  const valid =
    !!key && !badName && !dupName && (fromManager ? !!ms.source && !!path.trim() : keeps || (!!password && !shortPassword));

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    // Rendered in a portal, but React still bubbles the submit to the surrounding form.
    e.stopPropagation();
    if (!valid) return;
    const value = fromManager ? `{{ ${ms.source}://${path.trim().replace(/^\/+/, "")} }}` : password;
    onDone({ name: key, password: value, stored: !!user?.stored && !renamed });
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          <UserRound />
          {user ? `Edit ${user.name}` : "New user"}
        </DialogTitle>
        <DialogDescription>
          {fromManager
            ? "Stays in your vault: fetched on each deploy, never stored by kipitiny."
            : "Stored hashed: it can't be read back."}
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        {(ms.any || fromManager) && (
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            spacing={0}
            value={fromManager ? "manager" : "password"}
            onValueChange={(v) => v && setFromManager(v === "manager")}
            aria-label="Password source"
          >
            <ToggleGroupItem value="password" className="aria-checked:bg-muted">
              <KeyRound />
              Password
            </ToggleGroupItem>
            <ToggleGroupItem value="manager" className="aria-checked:bg-muted">
              <Vault />
              Password manager
            </ToggleGroupItem>
          </ToggleGroup>
        )}
        <FloatingInput
          label="User"
          required
          autoFocus={!user}
          autoComplete="off"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="admin"
          spellCheck={false}
          inputClassName="font-mono"
          aria-invalid={badName || dupName || undefined}
          description={
            badName ? (
              <span className="text-destructive">Letters, digits and . _ @ - (max 64).</span>
            ) : dupName ? (
              <span className="text-destructive">Another user has this name.</span>
            ) : undefined
          }
        />
        {fromManager ? (
          <ManagerSourceFields ms={ms} path={path} onPathChange={setPath} />
        ) : (
          <div className="relative">
            <FloatingInput
              label="Password"
              type={shown ? "text" : "password"}
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={savedPassword && !renamed ? "Unchanged" : `At least ${minPassword} characters`}
              spellCheck={false}
              aria-invalid={shortPassword || undefined}
              inputClassName="pr-12 font-mono text-sm"
              description={
                shortPassword ? (
                  <span className="text-destructive">At least {minPassword} characters.</span>
                ) : renamed && !password ? (
                  "Renaming the user needs a new password."
                ) : savedPassword ? (
                  "Saved. Leave empty to keep it, or type a new one to replace it."
                ) : undefined
              }
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={shown ? "Hide password" : "Show password"}
              title={shown ? "Hide password" : "Show password"}
              onClick={() => setShown(!shown)}
              className="absolute top-7 right-2 -translate-y-1/2"
            >
              {shown ? <EyeOff /> : <Eye />}
            </Button>
          </div>
        )}
      </div>
      <DialogFooter>
        <Button type="submit" disabled={!valid}>
          {user ? "Done" : "Add user"}
        </Button>
      </DialogFooter>
    </form>
  );
}
