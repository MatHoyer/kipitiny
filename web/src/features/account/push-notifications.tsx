import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing, Smartphone, Trash2, TriangleAlert } from "lucide-react";
import { Link } from "react-router";
import { ErrorText, Tag } from "@/components/common";
import { Section } from "@kipit-labs/kipitidy/section";
import { ConfirmDialog } from "@kipit-labs/kipitidy/confirm-dialog";
import { Button } from "@kipit-labs/kipitidy/button";
import { timeAgo } from "@/lib/format";
import { currentPushSubscription, enablePush, pushNeedsInstall, pushSupported } from "@/lib/push";
import { api } from "@/api";

/** The admin's browsers and phones that get the push channels' notifications. */
export function PushNotifications() {
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
