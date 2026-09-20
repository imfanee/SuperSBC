import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { get } from "@/api/client";
import type { Simulation } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/form";
import { KV, PageHeader } from "@/components/page";
import { CustomerSelect } from "@/components/selects";
import { money } from "@/lib/utils";

export function SimulatorPage() {
  const [params] = useSearchParams();
  const [customerId, setCustomerId] = useState<string | null>(params.get("customer_id"));
  const [number, setNumber] = useState(params.get("number") ?? "");
  const [caller, setCaller] = useState("");
  const sim = useMutation({
    mutationFn: () => get<Simulation>("/routing/simulate", { customer_id: customerId ?? "", number, caller }),
    onError: (e) => toast.error(e.message),
  });
  const r = sim.data;
  return (
    <div>
      <PageHeader
        title="Routing simulator"
        description="The whole setup decision without placing a call: authorisation, normalisation, rate, reservation, route and carriers."
      />
      <Card className="mb-4">
        <CardContent className="pt-4">
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              sim.mutate();
            }}
          >
            <Field label="Customer" className="w-64">
              <CustomerSelect
                value={customerId}
                onChange={setCustomerId}
                allowNone={false}
                testId="sim-customer"
              />
            </Field>
            <Field label="Number as dialled">
              <Input
                value={number}
                onChange={(e) => setNumber(e.target.value)}
                placeholder="00447700900123"
                className="w-56 font-mono"
                data-testid="sim-number"
              />
            </Field>
            <Field label="Caller (optional)">
              <Input value={caller} onChange={(e) => setCaller(e.target.value)} className="w-40 font-mono" />
            </Field>
            <Button type="submit" disabled={!customerId || !number || sim.isPending} data-testid="sim-submit">
              Simulate
            </Button>
          </form>
        </CardContent>
      </Card>
      {r && (
        <div className="grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>
                Decision path: <span data-testid="sim-result">{r.expected_sip}</span>
              </CardTitle>
              {r.modes && (
                <div className="text-xs text-muted-foreground">
                  Routing modes:{" "}
                  {[
                    r.modes.quality && "quality",
                    r.modes.lcr && "least cost",
                    r.modes.lossless && "lossless",
                    r.modes.percent && "percentage",
                  ]
                    .filter(Boolean)
                    .join(", ") || "priority"}
                </div>
              )}
            </CardHeader>
            <CardContent>
              <ol className="space-y-2">
                {r.steps.map((s) => (
                  <li key={s.step} className="flex gap-2 text-sm">
                    <Badge variant={s.ok ? "success" : "danger"} className="mt-0.5 h-5">
                      {s.ok ? "ok" : "stop"}
                    </Badge>
                    <div className="min-w-0 flex-1">
                      <div className="font-medium capitalize">{s.step}</div>
                      <StepDetail step={s.step} detail={s.detail} />
                    </div>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Carriers in dial order</CardTitle>
            </CardHeader>
            <CardContent>
              {(r.carriers ?? []).length === 0 && (
                <div className="text-sm text-muted-foreground">Nothing would be dialled.</div>
              )}
              <ol className="space-y-2">
                {(r.carriers ?? []).map((c) => (
                  <li key={c.seq} className="rounded-md border p-2 text-sm">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant="secondary">#{c.seq}</Badge>
                      <span className="font-medium">{c.name}</span>
                      {c.degraded && <Badge variant="warning">degraded</Badge>}
                      {c.negative_margin && <Badge variant="danger">negative margin</Badge>}
                      <span className="ml-auto text-xs text-muted-foreground">
                        priority {c.priority}, weight {c.weight}
                        {c.share ? `, share ${c.share}%` : ""}
                        {c.quality !== undefined && c.quality !== null
                          ? `, quality ${Number(c.quality).toFixed(2)}`
                          : ""}
                      </span>
                    </div>
                    <KV
                      items={[
                        ["Dial", <span className="font-mono">{c.dial_number}</span>],
                        ["Caller ID", <span className="font-mono">{c.caller_id}</span>],
                        ["Buy rate", `${money(c.buy_rate_per_min, 6)}/min ${c.buy_destination}`],
                        [
                          "Margin per minute",
                          <span className={Number(c.margin_per_min) < 0 ? "text-danger" : "text-success"}>
                            {money(c.margin_per_min, 6)}
                          </span>,
                        ],
                      ]}
                    />
                    <details className="mt-1 text-xs text-muted-foreground">
                      <summary className="cursor-pointer">dial string</summary>
                      <code className="break-all">{c.dial_string}</code>
                    </details>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}

function StepDetail({ step, detail }: { step: string; detail: unknown }) {
  if (detail === null || detail === undefined) return null;
  const d = detail as Record<string, unknown>;
  const rate = d.rate as Record<string, unknown> | undefined;
  switch (step) {
    case "authorize":
      return (
        <div className="text-xs text-muted-foreground">
          status {String(d.status)}, {String(d.ip_count)} IPs, limits {String(d.max_concurrent_calls) || "∞"}{" "}
          calls / {String(d.max_cps) || "∞"} cps
        </div>
      );
    case "normalize":
      return (
        <div className="text-xs text-muted-foreground">
          {d.error ? (
            String(d.error)
          ) : (
            <>
              normalised to <span className="font-mono">{String(d.called)}</span>
            </>
          )}
        </div>
      );
    case "blocklist":
      return (
        <div className="text-xs text-muted-foreground">
          {d.prefix ? `blocked by prefix ${String(d.prefix)} (${String(d.reason)})` : "not blocked"}
        </div>
      );
    case "rate":
      return rate ? (
        <div className="text-xs text-muted-foreground">
          {String(rate.destination)} prefix {String(rate.prefix)}: {money(String(rate.rate_per_min), 6)}/min +{" "}
          {money(String(rate.connect_fee), 6)}, increments {String(rate.initial_increment)}/
          {String(rate.subsequent_increment)}
          {d.currency ? ` in ${String(d.currency)} (fx ${String(d.fx)})` : ""}
        </div>
      ) : (
        <div className="text-xs text-danger">{String(d.error ?? "no rate")}</div>
      );
    case "balance":
      return (
        <div className="text-xs text-muted-foreground">
          available {money(String(d.available), 6)}, reservation {money(String(d.reserve_amount), 6)}, max
          call {String(d.max_call_seconds)} s
        </div>
      );
    case "route": {
      const route = d.route as Record<string, unknown> | null;
      const skipped = (d.skipped as Array<Record<string, unknown>> | null) ?? [];
      return (
        <div className="text-xs text-muted-foreground">
          {route ? `route ${String(route.prefix) || "(default)"} ${String(route.destination)}` : "no route"}
          {skipped.length > 0 && (
            <div>skipped: {skipped.map((s) => `${String(s.name)} (${String(s.reason)})`).join(", ")}</div>
          )}
        </div>
      );
    }
    default:
      return <pre className="text-xs text-muted-foreground">{JSON.stringify(detail)}</pre>;
  }
}
