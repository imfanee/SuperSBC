import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Plus, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { get, post, put } from "@/api/client";
import type { CarrierEndpoint } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useAuth } from "@/hooks/use-auth";

type Draft = {
  id?: string;
  host: string;
  port: number;
  transport: string;
  priority: number;
  weight: number;
  enabled: boolean;
  notes: string;
  gateway?: string;
  state?: string;
  parked?: boolean;
  parked_for_seconds?: number;
};

/** Signalling endpoints of a carrier: load balancing, failover and health (D-75). */
export function EndpointsTab({ carrierId, lbMode }: { carrierId: string; lbMode: string }) {
  const { can } = useAuth();
  const qc = useQueryClient();
  const key = ["endpoints", carrierId];
  const q = useQuery({
    queryKey: key,
    queryFn: () => get<CarrierEndpoint[]>(`/carriers/${carrierId}/endpoints`),
    refetchInterval: 15_000,
  });
  const [rows, setRows] = useState<Draft[]>([]);
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    if (q.data && !dirty) setRows(q.data.map((e) => ({ ...e })));
  }, [q.data, dirty]);
  const edit = (i: number, patch: Partial<Draft>) => {
    setDirty(true);
    setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  };
  const move = (i: number, by: number) => {
    const j = i + by;
    if (j < 0 || j >= rows.length) return;
    const next = [...rows];
    [next[i], next[j]] = [next[j], next[i]];
    setDirty(true);
    setRows(next.map((r, k) => ({ ...r, priority: k + 1 })));
  };
  const save = () =>
    put(`/carriers/${carrierId}/endpoints`, {
      endpoints: rows.map((r, i) => ({
        host: r.host.trim(),
        port: Number(r.port) || 5060,
        transport: r.transport,
        priority: i + 1,
        weight: Number(r.weight),
        enabled: r.enabled,
        notes: r.notes,
      })),
    })
      .then(() => {
        toast.success("Endpoints saved");
        setDirty(false);
        qc.invalidateQueries({ queryKey: key });
      })
      .catch((e) => toast.error(e.message));
  const weighted = lbMode === "weighted";
  const total = rows.reduce((s, r) => s + (Number(r.weight) || 0), 0);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Signalling endpoints</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Every endpoint is its own FreeSWITCH gateway, so a call that gets no answer from one address rolls
          over to the next before the next carrier is tried. With OPTIONS ping on, an address that stops
          answering pings is skipped automatically; with ping off, an address that stops answering calls is
          parked for the carrier's retry period and then tried again.
          {lbMode === "failover" && " Order decides: the first usable address is tried first."}
          {lbMode === "round_robin" &&
            " Calls are spread evenly over the addresses; the rest stay as failover."}
          {weighted && " Calls are spread by weight; the rest stay as failover."}
        </p>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground">
              <tr>
                <th className="py-1 text-left">Address</th>
                <th className="py-1 text-left">Port</th>
                <th className="py-1 text-left">Transport</th>
                {weighted && <th className="py-1 text-right">Weight</th>}
                <th className="py-1 text-left">Gateway</th>
                <th className="py-1 text-left">State</th>
                <th className="py-1 text-left">Enabled</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={r.id ?? `new-${i}`} className="border-t" data-testid="endpoint-row">
                  <td className="py-1 pr-2">
                    <Input
                      className="h-7 w-44 font-mono"
                      value={r.host}
                      onChange={(e) => edit(i, { host: e.target.value })}
                      disabled={!can("write")}
                      data-testid={`endpoint-host-${i}`}
                    />
                  </td>
                  <td className="py-1 pr-2">
                    <Input
                      className="h-7 w-20"
                      value={r.port}
                      onChange={(e) => edit(i, { port: Number(e.target.value) || 0 })}
                      disabled={!can("write")}
                    />
                  </td>
                  <td className="py-1 pr-2">
                    <Select
                      value={r.transport}
                      onValueChange={(v) => edit(i, { transport: v })}
                      disabled={!can("write")}
                    >
                      <SelectTrigger className="h-7 w-24">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {["udp", "tcp", "tls"].map((t) => (
                          <SelectItem key={t} value={t}>
                            {t}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </td>
                  {weighted && (
                    <td className="py-1 pr-2 text-right">
                      <Input
                        className="h-7 w-16 text-right"
                        value={r.weight}
                        onChange={(e) => edit(i, { weight: Number(e.target.value) || 0 })}
                        disabled={!can("write")}
                      />
                    </td>
                  )}
                  <td className="py-1 pr-2 font-mono text-xs text-muted-foreground">
                    {r.gateway ?? "(new)"}
                  </td>
                  <td className="py-1 pr-2">
                    {r.parked ? (
                      <span className="flex items-center gap-1">
                        <Badge variant="danger">parked</Badge>
                        <span className="text-xs text-muted-foreground">
                          {Math.ceil((r.parked_for_seconds ?? 0) / 60)} min
                        </span>
                        {can("write") && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-6 px-1"
                            aria-label="Put back into rotation"
                            onClick={() =>
                              post(`/carriers/${carrierId}/endpoints/${r.id}/revive`)
                                .then(
                                  () => (
                                    toast.success("Back in rotation"),
                                    qc.invalidateQueries({ queryKey: key })
                                  ),
                                )
                                .catch((e) => toast.error(e.message))
                            }
                          >
                            <RotateCcw className="size-3.5" />
                          </Button>
                        )}
                      </span>
                    ) : (
                      <Badge variant={stateVariant(r.state ?? "UNKNOWN")}>{r.state ?? "UNKNOWN"}</Badge>
                    )}
                  </td>
                  <td className="py-1 pr-2">
                    <Switch
                      checked={r.enabled}
                      onCheckedChange={(v) => edit(i, { enabled: v })}
                      disabled={!can("write")}
                    />
                  </td>
                  <td className="py-1 text-right">
                    {can("write") && (
                      <span className="flex justify-end gap-0.5">
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label="Move up"
                          disabled={i === 0}
                          onClick={() => move(i, -1)}
                        >
                          <ArrowUp />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label="Move down"
                          disabled={i === rows.length - 1}
                          onClick={() => move(i, 1)}
                        >
                          <ArrowDown />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label="Remove"
                          disabled={rows.length === 1}
                          onClick={() => (setDirty(true), setRows(rows.filter((_, j) => j !== i)))}
                        >
                          <Trash2 className="text-danger" />
                        </Button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {weighted && rows.length > 0 && (
          <div className="text-xs text-muted-foreground">
            Weights total {total}; calls are shared in proportion.
          </div>
        )}
        {can("write") && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => (
                setDirty(true),
                setRows([
                  ...rows,
                  {
                    host: "",
                    port: 5060,
                    transport: "udp",
                    priority: rows.length + 1,
                    weight: 100,
                    enabled: true,
                    notes: "",
                  },
                ])
              )}
              data-testid="endpoint-add"
            >
              <Plus /> Add endpoint
            </Button>
            <Button
              size="sm"
              disabled={!dirty || rows.some((r) => !r.host.trim())}
              onClick={save}
              data-testid="endpoint-save"
            >
              Save endpoints
            </Button>
            {dirty && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => (setDirty(false), setRows((q.data ?? []).map((e) => ({ ...e }))))}
              >
                Discard
              </Button>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
