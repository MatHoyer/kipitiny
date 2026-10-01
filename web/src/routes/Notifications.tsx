import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Send, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { CheckboxField, Empty, Section, Tag } from "@/components/common";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FloatingInput } from "@/components/ui/floating-input";
import { FloatingSelect } from "@/components/ui/floating-select";
import { api, type ChannelInput, type NotificationChannel, type Notifications as NotificationsState } from "../api";

export function Notifications() {
  const qc = useQueryClient();
  const notifications = useQuery({
    queryKey: ["notifications"],
    queryFn: api.notifications,
  });
  const [editing, setEditing] = useState<NotificationChannel | "new" | null>(null);
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

  return (
    <Section
      title="Notifications"
      description="Where kipitiny reports failed deployments and backups, restarted services and new versions."
      actions={
        <Button variant="outline" size="sm" onClick={() => setEditing("new")}>
          <Plus data-icon="inline-start" />
          Add channel
        </Button>
      }
    >
      {data?.channels.length === 0 ? (
        <Empty>No channels yet.</Empty>
      ) : (
        <ul className="-my-2 divide-y">
          {data?.channels.map((ch) => (
            <li key={ch.id} className="flex items-center justify-between gap-4 py-2.5">
              <div className="min-w-0">
                <p className="flex items-center gap-2 text-sm font-medium">
                  {ch.name}
                  <Tag>{kindLabel(ch.kind)}</Tag>
                  {!ch.enabled && <Tag>paused</Tag>}
                </p>
                <p className="text-xs text-muted-foreground">
                  {ch.events.length === 0
                    ? "no events"
                    : ch.events.map((e) => data.events.find((t) => t.type === e)?.label ?? e).join(", ")}
                </p>
              </div>
              <div className="flex shrink-0 gap-0.5">
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
                <Button variant="ghost" size="icon-sm" title="Edit" aria-label="Edit" onClick={() => setEditing(ch)}>
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
            </li>
          ))}
        </ul>
      )}
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
          {editing && data && (
            <ChannelForm
              key={editing === "new" ? "new" : editing.id}
              channel={editing === "new" ? null : editing}
              meta={data}
              onDone={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </Section>
  );
}

function ChannelForm({
  channel,
  meta,
  onDone,
}: {
  channel: NotificationChannel | null;
  meta: NotificationsState;
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
          kind: meta.kinds[0]?.name ?? "",
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
        <DialogTitle>{channel ? `Edit ${channel.name}` : "New channel"}</DialogTitle>
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
        {!channel && meta.kinds.length > 1 && (
          <FloatingSelect
            label="Type"
            value={form.kind}
            onValueChange={(v) => setForm({ ...form, kind: v, config: {} })}
            options={meta.kinds.map((k) => ({ value: k.name, label: k.label }))}
          />
        )}
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
        <Button type="submit" disabled={save.isPending}>
          {channel ? "Save" : "Add channel"}
        </Button>
      </DialogFooter>
    </form>
  );
}
