import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip as RTooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Download } from "lucide-react";
import { download, get } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Field } from "@/components/form";
import { ErrorBox, KV, PageHeader } from "@/components/page";
import { CarrierSelect, CustomerSelect } from "@/components/selects";
import { money, pct } from "@/lib/utils";

interface Row {
  key: string;
  label: string;
  attempts: number;
  answered: number;
  rejected: number;
  failed: number;
  asr: number;
  ner: number;
  acd: number;
  minutes: number;
  revenue: string;
  cost: string;
  margin: string;
  margin_pct: number;
  pdd_avg_ms: number;
  pdd_p95_ms: number;
  short_calls: number;
}
interface QualityRow extends Row {
  mos_avg: number;
  loss_avg: number;
  jitter_avg: number;
  short_ratio: number;
  false_answer: number;
}
interface PeakRow {
  key: string;
  label: string;
  peak_concurrent: number;
  peak_at: string;
  peak_cps: number;
  peak_cps_at: string;
}
interface Statement {
  currency: string;
  opening_balance: string;
  closing_balance: string;
  topups: string;
  charges: string;
  costs: string;
  adjustments: string;
  refunds: string;
  calls: number;
  lines: Array<{
    id: number;
    type: string;
    amount: string;
    balance_after: string;
    description: string;
    created_at: string;
  }>;
}

const groupings: Array<[string, string]> = [
  ["total", "Total"],
  ["customer", "Customer"],
  ["carrier", "Carrier"],
  ["destination", "Destination"],
  ["prefix:2", "Prefix (2 digits)"],
  ["prefix:4", "Prefix (4 digits)"],
  ["hour", "Hour"],
  ["day", "Day"],
  ["month", "Month"],
  ["hour_of_day", "Hour of day"],
  ["sip_code", "SIP code"],
  ["hangup_cause", "Hangup cause"],
  ["disposition", "Disposition"],
  ["src_ip", "Source IP"],
  ["failover_depth", "Failover depth"],
];

function defaultFrom() {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() - 7);
  return d.toISOString().slice(0, 10);
}

function RangeBar({
  from,
  to,
  setFrom,
  setTo,
  children,
}: {
  from: string;
  to: string;
  setFrom: (v: string) => void;
  setTo: (v: string) => void;
  children?: React.ReactNode;
}) {
  return (
    <div className="mb-3 flex flex-wrap items-end gap-2 rounded-md border bg-card p-3">
      <Field label="From (UTC)">
        <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
      </Field>
      <Field label="To (UTC, exclusive)">
        <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
      </Field>
      {children}
    </div>
  );
}

function TrafficTable({ rows }: { rows: Row[] }) {
  return (
    <div className="overflow-x-auto rounded-md border bg-card">
      <table className="w-full text-sm">
        <thead className="text-xs text-muted-foreground">
          <tr>
            {[
              "Group",
              "Attempts",
              "Answered",
              "ASR",
              "NER",
              "ACD s",
              "Minutes",
              "Revenue",
              "Cost",
              "Margin",
              "Margin %",
              "PDD avg",
              "PDD p95",
              "Short (<6s)",
            ].map((h) => (
              <th key={h} className="px-2 py-1 text-right first:text-left">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className="border-t tabular">
              <td className="px-2 py-1 text-left">{r.label}</td>
              <td className="px-2 py-1 text-right">{r.attempts}</td>
              <td className="px-2 py-1 text-right">{r.answered}</td>
              <td className="px-2 py-1 text-right">{pct(r.asr)}</td>
              <td className="px-2 py-1 text-right">{pct(r.ner)}</td>
              <td className="px-2 py-1 text-right">{r.acd.toFixed(0)}</td>
              <td className="px-2 py-1 text-right">{r.minutes.toFixed(1)}</td>
              <td className="px-2 py-1 text-right">{money(r.revenue)}</td>
              <td className="px-2 py-1 text-right">{money(r.cost)}</td>
              <td className={`px-2 py-1 text-right ${Number(r.margin) < 0 ? "text-danger" : ""}`}>
                {money(r.margin)}
              </td>
              <td className="px-2 py-1 text-right">{pct(r.margin_pct)}</td>
              <td className="px-2 py-1 text-right">{r.pdd_avg_ms.toFixed(0)}</td>
              <td className="px-2 py-1 text-right">{r.pdd_p95_ms.toFixed(0)}</td>
              <td className="px-2 py-1 text-right">{r.short_calls}</td>
            </tr>
          ))}
          {rows.length === 0 && (
            <tr>
              <td colSpan={14} className="py-6 text-center text-muted-foreground">
                No data in this range.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function TrafficReport({
  preset,
}: {
  preset?: { group_by?: string; title: string; metric?: "attempts" | "minutes" | "revenue" | "margin" };
}) {
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState("");
  const [groupBy, setGroupBy] = useState(preset?.group_by ?? "day");
  const [customer, setCustomer] = useState<string | null>(null);
  const [carrier, setCarrier] = useState<string | null>(null);
  const [orderBy, setOrderBy] = useState(
    preset?.group_by === "day" || preset?.group_by === "hour" ? "key" : "attempts",
  );
  const query = {
    from,
    to,
    group_by: groupBy,
    order_by: orderBy,
    customer_id: customer ?? "",
    carrier_id: carrier ?? "",
    limit: 500,
  };
  const q = useQuery({
    queryKey: ["report-traffic", query],
    queryFn: () => get<Row[]>("/reports/traffic", query),
  });
  const rows = q.data ?? [];
  const metric = preset?.metric ?? "attempts";
  const chart = rows.slice(0, 60).map((r) => ({
    label: r.label,
    attempts: r.attempts,
    answered: r.answered,
    minutes: Number(r.minutes.toFixed(1)),
    revenue: Number(r.revenue),
    cost: Number(r.cost),
    margin: Number(r.margin),
  }));
  return (
    <div>
      <RangeBar from={from} to={to} setFrom={setFrom} setTo={setTo}>
        <Field label="Group by">
          <Select value={groupBy} onValueChange={setGroupBy}>
            <SelectTrigger className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {groupings.map(([v, l]) => (
                <SelectItem key={v} value={v}>
                  {l}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Order by">
          <Select value={orderBy} onValueChange={setOrderBy}>
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["attempts", "key", "minutes", "revenue", "cost", "margin", "asr"].map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label="Customer" className="w-44">
          <CustomerSelect value={customer} onChange={setCustomer} />
        </Field>
        <Field label="Carrier" className="w-44">
          <CarrierSelect value={carrier} onChange={setCarrier} />
        </Field>
        <Button
          variant="outline"
          className="ml-auto"
          onClick={() => download("/reports/traffic", `traffic-${groupBy}.csv`, { ...query, format: "csv" })}
        >
          <Download /> CSV
        </Button>
      </RangeBar>
      {q.error && <ErrorBox error={q.error} />}
      <Card className="mb-3">
        <CardHeader>
          <CardTitle>{preset?.title ?? "Traffic"}</CardTitle>
        </CardHeader>
        <CardContent className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            {metric === "margin" || metric === "revenue" ? (
              <BarChart data={chart}>
                <CartesianGrid strokeDasharray="3 3" opacity={0.3} />
                <XAxis dataKey="label" tick={{ fontSize: 10 }} />
                <YAxis tick={{ fontSize: 10 }} />
                <RTooltip />
                <Legend />
                <Bar dataKey="revenue" fill="#4F46E5" name="Revenue" />
                <Bar dataKey="cost" fill="#f43f5e" name="Cost" />
                <Bar dataKey="margin" fill="#10b981" name="Margin" />
              </BarChart>
            ) : (
              <BarChart data={chart}>
                <CartesianGrid strokeDasharray="3 3" opacity={0.3} />
                <XAxis dataKey="label" tick={{ fontSize: 10 }} />
                <YAxis tick={{ fontSize: 10 }} />
                <RTooltip />
                <Legend />
                <Bar dataKey="attempts" fill="#4F46E5" name="Attempts" />
                <Bar dataKey="answered" fill="#10b981" name="Answered" />
                <Bar dataKey="minutes" fill="#0ea5e9" name="Minutes" />
              </BarChart>
            )}
          </ResponsiveContainer>
        </CardContent>
      </Card>
      <TrafficTable rows={rows} />
    </div>
  );
}

function QualityReport() {
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState("");
  const [groupBy, setGroupBy] = useState("carrier");
  const q = useQuery({
    queryKey: ["report-quality", from, to, groupBy],
    queryFn: () => get<QualityRow[]>("/reports/quality", { from, to, group_by: groupBy }),
  });
  const rows = q.data ?? [];
  const chart = rows.map((r) => ({
    label: r.label,
    asr: Number((r.asr * 100).toFixed(1)),
    acd: Number(r.acd.toFixed(0)),
    pdd: Number(r.pdd_avg_ms.toFixed(0)),
  }));
  return (
    <div>
      <RangeBar from={from} to={to} setFrom={setFrom} setTo={setTo}>
        <Field label="Group by">
          <Select value={groupBy} onValueChange={setGroupBy}>
            <SelectTrigger className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["carrier", "customer", "destination", "total"].map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </RangeBar>
      {q.error && <ErrorBox error={q.error} />}
      <Card className="mb-3">
        <CardHeader>
          <CardTitle>ASR %, ACD s and PDD ms per group and day</CardTitle>
        </CardHeader>
        <CardContent className="h-64">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={chart}>
              <CartesianGrid strokeDasharray="3 3" opacity={0.3} />
              <XAxis dataKey="label" tick={{ fontSize: 10 }} />
              <YAxis tick={{ fontSize: 10 }} />
              <RTooltip />
              <Legend />
              <Line dataKey="asr" stroke="#10b981" name="ASR %" dot={false} />
              <Line dataKey="acd" stroke="#4F46E5" name="ACD s" dot={false} />
              <Line dataKey="pdd" stroke="#f59e0b" name="PDD ms" dot={false} />
            </LineChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
      <div className="overflow-x-auto rounded-md border bg-card">
        <table className="w-full text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr>
              {[
                "Group / day",
                "Attempts",
                "ASR",
                "ACD s",
                "PDD avg",
                "PDD p95",
                "MOS",
                "Loss",
                "Jitter",
                "Short ratio",
                "False answer",
              ].map((h) => (
                <th key={h} className="px-2 py-1 text-right first:text-left">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.key} className="border-t tabular">
                <td className="px-2 py-1 text-left">{r.label}</td>
                <td className="px-2 py-1 text-right">{r.attempts}</td>
                <td className="px-2 py-1 text-right">{pct(r.asr)}</td>
                <td className="px-2 py-1 text-right">{r.acd.toFixed(0)}</td>
                <td className="px-2 py-1 text-right">{r.pdd_avg_ms.toFixed(0)}</td>
                <td className="px-2 py-1 text-right">{r.pdd_p95_ms.toFixed(0)}</td>
                <td className="px-2 py-1 text-right">{r.mos_avg ? r.mos_avg.toFixed(2) : ""}</td>
                <td className="px-2 py-1 text-right">{pct(r.loss_avg)}</td>
                <td className="px-2 py-1 text-right">{r.jitter_avg.toFixed(1)}</td>
                <td className="px-2 py-1 text-right">{pct(r.short_ratio)}</td>
                <td className="px-2 py-1 text-right">{r.false_answer}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function PeaksReport() {
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState("");
  const [groupBy, setGroupBy] = useState("system");
  const q = useQuery({
    queryKey: ["report-peaks", from, to, groupBy],
    queryFn: () => get<PeakRow[]>("/reports/peaks", { from, to, group_by: groupBy }),
  });
  return (
    <div>
      <RangeBar from={from} to={to} setFrom={setFrom} setTo={setTo}>
        <Field label="Group by">
          <Select value={groupBy} onValueChange={setGroupBy}>
            <SelectTrigger className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["system", "customer", "carrier"].map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </RangeBar>
      {q.error && <ErrorBox error={q.error} />}
      <div className="overflow-x-auto rounded-md border bg-card">
        <table className="w-full text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr>
              <th className="px-2 py-1 text-left">Scope</th>
              <th className="px-2 py-1 text-right">Peak concurrent calls</th>
              <th className="px-2 py-1 text-left">at</th>
              <th className="px-2 py-1 text-right">Peak CPS</th>
              <th className="px-2 py-1 text-left">at</th>
            </tr>
          </thead>
          <tbody>
            {(q.data ?? []).map((r) => (
              <tr key={r.key} className="border-t tabular">
                <td className="px-2 py-1">{r.label}</td>
                <td className="px-2 py-1 text-right">{r.peak_concurrent}</td>
                <td className="px-2 py-1">{new Date(r.peak_at).toLocaleString()}</td>
                <td className="px-2 py-1 text-right">{r.peak_cps}</td>
                <td className="px-2 py-1">{new Date(r.peak_cps_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function StatementReport() {
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState("");
  const [ownerType, setOwnerType] = useState<"customer" | "carrier">("customer");
  const [owner, setOwner] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ["report-statement", from, to, ownerType, owner],
    queryFn: () =>
      get<Statement>("/reports/statement", { from, to, owner_type: ownerType, owner_id: owner ?? "" }),
    enabled: !!owner,
  });
  const s = q.data;
  return (
    <div>
      <RangeBar from={from} to={to} setFrom={setFrom} setTo={setTo}>
        <Field label="Account type">
          <Select
            value={ownerType}
            onValueChange={(v) => (setOwnerType(v as "customer" | "carrier"), setOwner(null))}
          >
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="customer">customer</SelectItem>
              <SelectItem value="carrier">carrier</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label={ownerType} className="w-56">
          {ownerType === "customer" ? (
            <CustomerSelect value={owner} onChange={setOwner} allowNone={false} />
          ) : (
            <CarrierSelect value={owner} onChange={setOwner} allowNone={false} />
          )}
        </Field>
      </RangeBar>
      {q.error && <ErrorBox error={q.error} />}
      {s && (
        <div className="grid gap-4 lg:grid-cols-3">
          <Card>
            <CardHeader>
              <CardTitle>Statement ({s.currency})</CardTitle>
            </CardHeader>
            <CardContent>
              <KV
                items={[
                  ["Opening balance", money(s.opening_balance)],
                  ["Top-ups", money(s.topups)],
                  ["Charges", money(s.charges)],
                  ["Costs", money(s.costs)],
                  ["Adjustments", money(s.adjustments)],
                  ["Refunds", money(s.refunds)],
                  ["Closing balance", <b>{money(s.closing_balance)}</b>],
                  ["Calls billed", s.calls],
                ]}
              />
            </CardContent>
          </Card>
          <Card className="lg:col-span-2">
            <CardHeader>
              <CardTitle>Movements</CardTitle>
            </CardHeader>
            <CardContent className="max-h-96 overflow-auto">
              <table className="w-full text-xs">
                <tbody>
                  {s.lines.map((l) => (
                    <tr key={l.id} className="border-t tabular">
                      <td className="py-1 text-muted-foreground">
                        {new Date(l.created_at).toLocaleString()}
                      </td>
                      <td className="py-1">{l.type}</td>
                      <td
                        className={`py-1 text-right ${Number(l.amount) < 0 ? "text-danger" : "text-success"}`}
                      >
                        {money(l.amount)}
                      </td>
                      <td className="py-1 text-right">{money(l.balance_after)}</td>
                      <td className="py-1 text-muted-foreground">{l.description}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}

export function ReportsPage() {
  return (
    <div>
      <PageHeader title="Reports" description="SQL over CDRs. Every table exports to CSV." />
      <Tabs defaultValue="traffic">
        <TabsList>
          <TabsTrigger value="traffic">Traffic</TabsTrigger>
          <TabsTrigger value="customers">By customer</TabsTrigger>
          <TabsTrigger value="carriers">By carrier</TabsTrigger>
          <TabsTrigger value="destinations">By destination</TabsTrigger>
          <TabsTrigger value="profit">Profitability</TabsTrigger>
          <TabsTrigger value="failures">Failure analysis</TabsTrigger>
          <TabsTrigger value="rejections">Rejections</TabsTrigger>
          <TabsTrigger value="quality">Quality</TabsTrigger>
          <TabsTrigger value="peaks">Peaks</TabsTrigger>
          <TabsTrigger value="statement">Statement</TabsTrigger>
          <TabsTrigger value="top">Top N</TabsTrigger>
        </TabsList>
        <TabsContent value="traffic">
          <TrafficReport preset={{ group_by: "day", title: "Daily traffic profile" }} />
        </TabsContent>
        <TabsContent value="customers">
          <TrafficReport preset={{ group_by: "customer", title: "Traffic by customer" }} />
        </TabsContent>
        <TabsContent value="carriers">
          <TrafficReport preset={{ group_by: "carrier", title: "Traffic by carrier" }} />
        </TabsContent>
        <TabsContent value="destinations">
          <TrafficReport preset={{ group_by: "destination", title: "Traffic by destination" }} />
        </TabsContent>
        <TabsContent value="profit">
          <TrafficReport
            preset={{ group_by: "customer", title: "Revenue, cost and margin", metric: "margin" }}
          />
        </TabsContent>
        <TabsContent value="failures">
          <TrafficReport preset={{ group_by: "sip_code", title: "Final SIP codes" }} />
        </TabsContent>
        <TabsContent value="rejections">
          <TrafficReport
            preset={{
              group_by: "disposition",
              title: "Rejections by disposition (group by source IP or prefix for detail)",
            }}
          />
        </TabsContent>
        <TabsContent value="quality">
          <QualityReport />
        </TabsContent>
        <TabsContent value="peaks">
          <PeaksReport />
        </TabsContent>
        <TabsContent value="statement">
          <StatementReport />
        </TabsContent>
        <TabsContent value="top">
          <TrafficReport
            preset={{ group_by: "destination", title: "Top destinations by minutes", metric: "minutes" }}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}
