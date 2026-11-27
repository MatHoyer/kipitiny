import { type FormEvent, type ReactNode } from "react";
import { ConfirmField, useConfirmation } from "@/features/auth/confirm-identity";
import { Button } from "@kipit-labs/kipitidy/button";
import { DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kipit-labs/kipitidy/dialog";

/** Asks for the account password (or a recent sign-in, without one) before a change to how it signs in. */
export function ConfirmPassword({
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
