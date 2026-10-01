import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, ChevronLeft, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";
import { DiscordIcon } from "@/components/brand-icons";
import { CheckboxField, ChoiceTile, EmptyState, ErrorText, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { cn } from "@/lib/utils";
import { api, type ChannelInput, type NotificationChannel, type Notifications as NotificationsState } from "@/api";
import { SettingsPage } from "./page";

/** Brand marks by channel kind; others get a bell. */
const kindIcons: Record<string, ReactNode> = {
  discord: <DiscordIcon className="text-[#5865F2]" />,
};
const kindIcon = (kind: string) => kindIcons[kind] ?? <Bell />;

/** What the dialog shows: the kind picker, a new channel of a kind, or a channel being edited. */
type Editing = { step: "pick" } | { step: "new"; kind: string } | { step: "edit"; channel: NotificationChannel };

export function Notifications() {
  const qc = useQueryClient();
  const notifications = useQuery({
    queryKey: ["notifications"],
    queryFn: api.notifications,
  });
  const [editing, setEditing] = useState<Editing | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["notifications"] });
  const remove = useMutation({
    meta: { error: "Couldn't remove the channel" },
    mutationFn: api.deleteChannel,
    onSuccess: refresh,
  });
  const test = useMutation({
    meta: { error: "The test notification wasn't delivered" },
    mutationFn: api.testChannel,
    onSuccess: () => toast.success("Test notification sent"),
  });
  const data = notifications.data;
  const kindLabel = (name: string) => data?.kinds.find((k) => k.name === name)?.label ?? name;
  const eventLabel = (type: string) => data?.events.find((t) => t.type === type)?.label ?? type;
  const add = (
    <Button size="sm" onClick={() => setEditing({ step: "pick" })}>
      <Plus data-icon="inline-start" />
      Add channel
    </Button>
  );

  return (
    <SettingsPage actions={!!data?.channels.length && add}>
      <ErrorText error={notifications.error} />
      {data?.channels.length === 0 ? (
        <EmptyState
          icon={Bell}
          title="No notification channels"
          description="Get told about failed deployments and backups, restarted services and new versions."
          action={add}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {data?.channels.map((ch) => (
            <Card key={ch.id} className={cn("gap-3 px-4", !ch.enabled && "opacity-70")}>
              <div className="flex items-start gap-3">
                <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted [&>svg]:size-5">
                  {kindIcon(ch.kind)}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">{ch.name}</p>
                  <p className="text-xs text-muted-foreground">
                    {kindLabel(ch.kind)}
                    {!ch.enabled && " · paused"}
                  </p>
                </div>
              </div>
              <div className="flex flex-wrap gap-1">
                {ch.events.length === 0 ? (
                  <span className="text-xs text-muted-foreground">No events</span>
                ) : (
                  ch.events.map((e) => <Tag key={e}>{eventLabel(e)}</Tag>)
                )}
              </div>
              <div className="mt-auto flex justify-end gap-0.5">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  title="Send a test"
                  aria-label="Send a test"
                  disabled={test.isPending}
                  onClick={() => test.mutate(ch.id)}
                >
                  <Send />
                </Button>
                <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={() => setEditing({ step: "edit", channel: ch })}>
                  <Pencil />
                </Button>
                <ConfirmDialog
                  trigger={
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      title="Remove"
                      aria-label="Remove"
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <Trash2 />
                    </Button>
                  }
                  title={`Remove channel ${ch.name}?`}
                  confirmLabel="Remove"
                  onConfirm={() => remove.mutate(ch.id)}
                />
              </div>
            </Card>
          ))}
        </div>
      )}
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
          {editing && data && editing.step === "pick" && (
            <>
              <DialogHeader>
                <DialogTitle>Add a channel</DialogTitle>
                <DialogDescription>Where should kipitiny send notifications?</DialogDescription>
              </DialogHeader>
              <div className="grid gap-3 sm:grid-cols-2">
                {data.kinds.map((k) => (
                  <ChoiceTile
                    key={k.name}
                    icon={kindIcon(k.name)}
                    title={k.label}
                    onClick={() => setEditing({ step: "new", kind: k.name })}
                  />
                ))}
              </div>
            </>
          )}
          {editing && data && editing.step !== "pick" && (
            <ChannelForm
              key={editing.step === "new" ? editing.kind : editing.channel.id}
              channel={editing.step === "edit" ? editing.channel : null}
              kind={editing.step === "new" ? editing.kind : editing.channel.kind}
              icon={kindIcon(editing.step === "new" ? editing.kind : editing.channel.kind)}
              meta={data}
              onBack={editing.step === "new" ? () => setEditing({ step: "pick" }) : undefined}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </SettingsPage>
  );
}

function ChannelForm({
  channel,
  kind: kindName,
  icon,
  meta,
  onBack,
  onDone,
}: {
  channel: NotificationChannel | null;
  kind: string;
  icon: ReactNode;
  meta: NotificationsState;
  onBack?: () => void;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState<ChannelInput>(
    channel
      ? {
          name: channel.name,
          kind: channel.kind,
          config: channel.config,
          events: channel.events,
          enabled: channel.enabled,
        }
      : {
          name: "",
          kind: kindName,
          config: {},
          events: meta.events.filter((e) => e.default).map((e) => e.type),
          enabled: true,
        },
  );
  const kind = meta.kinds.find((k) => k.name === form.kind);
  const save = useMutation({
    meta: { error: "Couldn't save the channel" },
    mutationFn: () => (channel ? api.updateChannel(channel.id, form) : api.createChannel(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifications"] });
      onDone();
    },
  });
  const toggle = (type: string, on: boolean) =>
    setForm({
      ...form,
      events: on ? [...form.events, type] : form.events.filter((e) => e !== type),
    });
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <form onSubmit={onSubmit} className="contents">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2 [&>svg]:size-5">
          {icon}
          {channel ? `Edit ${channel.name}` : `New ${kind?.label ?? kindName} channel`}
        </DialogTitle>
        <DialogDescription>
          {form.kind === "discord"
            ? "In Discord: channel settings › Integrations › Webhooks › New Webhook, then copy its URL."
            : "Pick the events this channel receives."}
        </DialogDescription>
      </DialogHeader>
      <div className="space-y-4">
        <FloatingInput
          label="Name"
          required
          autoFocus
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          placeholder="ops-alerts"
        />
        {kind?.fields.map((f) => (
          <FloatingInput
            key={f.key}
            label={f.label}
            type={f.secret ? "password" : "text"}
            autoComplete="off"
            required={f.required}
            value={form.config[f.key] ?? ""}
            onChange={(e) =>
              setForm({
                ...form,
                config: { ...form.config, [f.key]: e.target.value },
              })
            }
            placeholder={f.placeholder}
          />
        ))}
        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">Events</legend>
          <div className="grid gap-2 sm:grid-cols-2">
            {meta.events.map((e) => (
              <CheckboxField
                key={e.type}
                label={e.label}
                checked={form.events.includes(e.type)}
                onCheckedChange={(on) => toggle(e.type, on)}
              />
            ))}
          </div>
        </fieldset>
        <CheckboxField
          label="Enabled"
          description="Pause a channel without losing its settings."
          checked={form.enabled}
          onCheckedChange={(enabled) => setForm({ ...form, enabled })}
        />
      </div>
      <DialogFooter>
        {onBack && (
          <Button type="button" variant="ghost" disabled={save.isPending} onClick={onBack}>
            <ChevronLeft data-icon="inline-start" />
            Back
          </Button>
        )}
        <Button type="submit" disabled={save.isPending}>
          {channel ? "Save" : "Add channel"}
        </Button>
      </DialogFooter>
    </form>
  );
}
