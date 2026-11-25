import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router";
import { AnalyticsPanel } from "@/components/analytics-tab";
import { Mono } from "@/components/common";
import { Logo } from "@/components/logo";
import { useApplyBranding, useBranding } from "@/lib/branding";
import { api, ApiError } from "../api";

/** An app's Analytics tab behind a shared link, without signing in: read-only, no visits. */
export function SharedAnalytics() {
  const { token = "" } = useParams();
  useApplyBranding(useBranding());
  // The domain comes with the stats; a 404 is a revoked or unknown link.
  const probe = useQuery({
    queryKey: ["shared-analytics-domain", token],
    queryFn: () => {
      const to = Math.ceil(Date.now() / 1000 / 60) * 60;
      return api.sharedAnalytics(token, { from: to - 60, to, interval: 60, offset: 0 });
    },
    retry: false,
  });
  const gone = probe.error instanceof ApiError && probe.error.status === 404;
  return (
    <div className="min-h-svh bg-background">
      <header className="border-b">
        <div className="mx-auto flex max-w-6xl items-center gap-3 px-4 py-3">
          <Logo className="text-lg" />
          <span className="text-sm text-muted-foreground">Shared analytics</span>
        </div>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-6">
        {gone ? (
          <div className="mx-auto max-w-md space-y-2 py-16 text-center">
            <h1 className="text-xl font-semibold">This link doesn't work anymore</h1>
            <p className="text-sm text-muted-foreground">It was revoked or replaced by a new one. Ask whoever shared it for the current link.</p>
          </div>
        ) : (
          <AnalyticsPanel
            queryKey={["shared-analytics", token]}
            load={(q) => api.sharedAnalytics(token, q)}
            intro={probe.data ? <>Visits of <Mono>{probe.data.domain}</Mono>, counted without cookies.</> : "Visits, counted without cookies."}
            readOnly
          />
        )}
      </main>
    </div>
  );
}
