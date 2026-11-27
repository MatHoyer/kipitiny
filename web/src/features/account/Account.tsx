import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { ErrorText } from "@/components/common";
import { Section } from "@kipit-labs/kipitidy/section";
import { ConfirmField, useConfirmation } from "@/features/auth/confirm-identity";
import { PageBody, PageHeader } from "@/features/layout/page-header";
import { Button } from "@kipit-labs/kipitidy/button";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { api, type Account } from "@/api";
import { Passkeys } from "./passkeys";
import { PushNotifications } from "./push-notifications";
import { SingleSignOn } from "./single-sign-on";
import { TwoFactor } from "./two-factor";

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
