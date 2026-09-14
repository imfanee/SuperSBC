import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Area, AreaChart, ResponsiveContainer, Tooltip as RTooltip, XAxis, YAxis } from "recharts";
import { get } from "@/api/client";
import type { CarrierStatus } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorBox, PageHeader, StatCard } from "@/components/page";
import { money, num, pct } from "@/lib/utils";

interface Row {
  key: string;
  label: string;
  attempts: number;
  answered: number;
  asr: number;
  acd: number;
  ner: number;
  minutes: number;
  revenue: string;
  cost: string;
  margin: string;
  margin_pct: number;
}
interface Summary {
  total: Row;
  live_calls: number;
  cps_now: number;
  hourly: Row[];
  top_destinations: Row[];
  rejections: Row[];
  low_balance: Array<{ customer_id: string; name: string; available: string; currency: string }>;
}

export function DashboardPage() {
  const summary = useQuery({
    queryKey: ["summary"],
    queryFn: () => get<Summary>("/reports/summary"),
    refetchInterval: 15_000,
  });
  const carriers = useQuery({
    queryKey: ["carriers-status"],
    queryFn: () => get<CarrierStatus[]>("/carriers/status"),
    refetchInterval: 15_000,
  });
  const t = summary.data?.total;
  const hourly = (summary.data?.hourly ?? []).map((h) => ({
    hour: h.label.slice(11),
    calls: h.attempts,
    answered: h.answered,
  }));
  return (
    <div className="space-y-4">
      <PageHeader title="Dashboard" description="Today, UTC. Refreshes every 15 seconds." />
      {summary.error && <ErrorBox error={summary.error} />}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
        <StatCard label="Calls today" value={num(t?.attempts)} hint={`${num(t?.answered)} answered`} />
        <StatCard
          label="ASR"
          value={pct(t?.asr)}
          hint={`NER ${pct(t?.ner)}`}
          tone={(t?.asr ?? 0) >= 0.4 ? "success" : "warning"}
        />
        <StatCard
          label="ACD"
          value={`${(t?.acd ?? 0).toFixed(0)} s`}
          hint={`${(t?.minutes ?? 0).toFixed(1)} min`}
        />
        <StatCard
          label="Live calls"
          value={num(summary.data?.live_calls)}
          hint={`${num(summary.data?.cps_now)} CPS now`}
          tone="info"
        />
        <StatCard label="Revenue" value={money(t?.revenue, 2)} />
        <StatCard label="Cost" value={money(t?.cost, 2)} />
        <StatCard
          label="Margin"
          value={money(t?.margin, 2)}
          hint={pct(t?.margin_pct)}
          tone={Number(t?.margin ?? 0) >= 0 ? "success" : "danger"}
        />
        <StatCard
          label="Carriers up"
          value={`${(carriers.data ?? []).filter((c) => c.state === "UP" || c.state === "NOPING").length} / ${carriers.data?.length ?? 0}`}
        />
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Calls per hour</CardTitle>
          </CardHeader>
          <CardContent className="h-56">
            {hourly.length === 0 ? (
              <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                No calls yet today.
              </div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={hourly} margin={{ left: 0, right: 8, top: 8, bottom: 0 }}>
                  <defs>
                    <linearGradient id="g1" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#4F46E5" stopOpacity={0.5} />
                      <stop offset="95%" stopColor="#4F46E5" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis dataKey="hour" tick={{ fontSize: 11 }} />
                  <YAxis tick={{ fontSize: 11 }} width={32} allowDecimals={false} />
                  <RTooltip />
                  <Area type="monotone" dataKey="calls" stroke="#4F46E5" fill="url(#g1)" name="Attempts" />
                  <Area type="monotone" dataKey="answered" stroke="#10b981" fill="none" name="Answered" />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Carrier health</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {(carriers.data ?? []).map((c) => (
              <Link
                key={c.carrier_id}
                to={`/carriers/${c.carrier_id}`}
                className="flex items-center justify-between rounded-md border p-2 text-sm hover:bg-accent"
              >
                <div>
                  <div className="font-medium">{c.name}</div>
                  <div className="text-xs text-muted-foreground">
                    {c.calls_in_progress ?? 0} live, {c.answered_5m ?? 0}/{c.attempts_5m ?? 0} answered (5 m)
                  </div>
                </div>
                <div className="flex gap-1">
                  {c.degraded && <Badge variant="warning">degraded</Badge>}
                  <Badge variant={stateVariant(c.state)}>{c.state}</Badge>
                </div>
              </Link>
            ))}
            {carriers.data?.length === 0 && (
              <div className="text-sm text-muted-foreground">No carriers configured.</div>
            )}
          </CardContent>
        </Card>
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>Top destinations (minutes)</CardTitle>
          </CardHeader>
          <CardContent>
            <table className="w-full text-sm">
              <tbody>
                {(summary.data?.top_destinations ?? []).map((d) => (
                  <tr key={d.key} className="border-b border-dashed last:border-0">
                    <td className="py-1">{d.label}</td>
                    <td className="py-1 text-right tabular">{d.minutes.toFixed(1)}</td>
                    <td className="py-1 text-right tabular text-muted-foreground">{pct(d.asr, 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Outcomes</CardTitle>
          </CardHeader>
          <CardContent>
            <table className="w-full text-sm">
              <tbody>
                {(summary.data?.rejections ?? []).map((d) => (
                  <tr key={d.key} className="border-b border-dashed last:border-0">
                    <td className="py-1">
                      <Badge variant={stateVariant(d.key)}>{d.label}</Badge>
                    </td>
                    <td className="py-1 text-right tabular">{num(d.attempts)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Low balance customers</CardTitle>
          </CardHeader>
          <CardContent>
            {(summary.data?.low_balance ?? []).length === 0 && (
              <div className="text-sm text-muted-foreground">Everyone is above the threshold.</div>
            )}
            <table className="w-full text-sm">
              <tbody>
                {(summary.data?.low_balance ?? []).map((c) => (
                  <tr key={c.customer_id} className="border-b border-dashed last:border-0">
                    <td className="py-1">
                      <Link to={`/customers/${c.customer_id}`} className="underline-offset-2 hover:underline">
                        {c.name}
                      </Link>
                    </td>
                    <td className="py-1 text-right tabular text-danger">
                      {money(c.available)} {c.currency}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
