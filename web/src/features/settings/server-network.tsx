import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Mono } from "@/components/common";
import { Section } from "@kipit-labs/kipitidy/section";
import { SaveBar } from "@/components/save-bar";
import { FloatingInput } from "@kipit-labs/kipitidy/floating-input";
import { api, SECRET_MASK, type Server } from "@/api";

/** How a server's apps are reached: a Cloudflare tunnel, or its public IP. */
export function NetworkSection({ server }: { server: Server }) {
  const qc = useQueryClient();
  const [ip, setIp] = useState(server.publicIp);
  const [token, setToken] = useState(server.tunnel === "server" ? SECRET_MASK : "");
  const save = useMutation({
    meta: { error: "Couldn't save the network settings" },
    mutationFn: () => api.setServerNetwork(server.id, { publicIp: ip.trim(), tunnelToken: token.trim() }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      qc.invalidateQueries({ queryKey: ["cloudflare"] });
      setIp(ip.trim());
      setToken(token.trim() ? SECRET_MASK : "");
      toast.success("Network settings saved");
    },
  });
  const formId = useId();
  const dirty = ip !== server.publicIp || token !== (server.tunnel === "server" ? SECRET_MASK : "");
  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };

  return (
    <Section title="Network" description="How its apps are reached from the internet.">
      <form id={formId} onSubmit={onSubmit} className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <FloatingInput
            label="Cloudflare tunnel token"
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            description={
              server.tunnel === "env" && !token ? (
                <>
                  Set by <Mono>KIPITINY_CLOUDFLARE_TUNNEL_TOKEN</Mono>; a token here replaces it.
                </>
              ) : (
                "Its own tunnel (one per server). With a token, cloudflared runs there and ports 80/443 aren't published. Empty for public ports."
              )
            }
          />
          <FloatingInput
            label="Public IPv4"
            value={ip}
            onChange={(e) => setIp(e.target.value)}
            placeholder={server.detectedIp || "203.0.113.10"}
            description={
              <>
                Where Cloudflare DNS records point without a tunnel. Empty to detect it
                {server.detectedIp && (
                  <>
                    {" "}
                    (currently <Mono>{server.detectedIp}</Mono>)
                  </>
                )}
                .
              </>
            }
          />
        </div>
      </form>
      <SaveBar
        form={formId}
        dirty={dirty}
        saving={save.isPending}
        onReset={() => {
          setIp(server.publicIp);
          setToken(server.tunnel === "server" ? SECRET_MASK : "");
        }}
      />
    </Section>
  );
}
