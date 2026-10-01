import { createContext, useContext, type ReactNode } from "react";
import { PageBody, PageHeader } from "@/components/page-header";

/** The current settings page's name, set by the shell. */
export const SettingsLabel = createContext("");

/** A settings page: the breadcrumb names it, so its blocks need no title; actions sit in the header. */
export function SettingsPage({ actions, children }: { actions?: ReactNode; children: ReactNode }) {
  const label = useContext(SettingsLabel);
  return (
    <>
      <PageHeader crumbs={[{ label: "Settings" }, { label }]} actions={actions} />
      <PageBody>{children}</PageBody>
    </>
  );
}
