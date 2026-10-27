import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Box, MapIcon } from "lucide-react";
import { Section } from "@/components/common";
import { PageBody, PageHeader } from "@/components/page-header";
import { Segmented, SegmentedItem } from "@/components/ui/segmented";
import { api, type AuthState, type Preferences } from "@/api";

/** The signed-in user's own UI choices, kept on the server for every browser. */
export function PreferencesPage() {
  const qc = useQueryClient();
  const auth = useQuery({ queryKey: ["auth"], queryFn: api.authState, staleTime: Infinity });
  const prefs = auth.data?.user?.preferences ?? {};
  const save = useMutation({
    meta: { error: "Couldn't save the preference" },
    mutationFn: api.setPreferences,
    onSuccess: (p) => qc.setQueryData<AuthState>(["auth"], (a) => a?.user && { ...a, user: { ...a.user, preferences: p } }),
  });
  const set = (p: Preferences) => save.mutate({ ...prefs, ...p });
  return (
    <>
      <PageHeader crumbs={[{ label: "Preferences" }]} />
      <PageBody className="max-w-3xl">
        <Section
          title="Map view"
          description="How the map opens, on the Map page and in projects. Its 3D button still switches for one visit."
          actions={
            <Segmented
              value={(save.isPending ? save.variables.mapView : prefs.mapView) ?? "2d"}
              onValueChange={(v) => set({ mapView: v === "3d" ? "3d" : undefined })}
              aria-label="Map view"
            >
              <SegmentedItem value="2d">
                <MapIcon />
                2D
              </SegmentedItem>
              <SegmentedItem value="3d">
                <Box />
                3D
              </SegmentedItem>
            </Segmented>
          }
        />
      </PageBody>
    </>
  );
}
