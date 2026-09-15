import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { Download, Save } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { download, get } from "@/api/client";
import type { CDR, Listing } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { DataTable } from "@/components/data-table";
import { KV, PageHeader } from "@/components/page";
import { CustomerSelect, CarrierSelect } from "@/components/selects";
import { dt, duration, money } from "@/lib/utils";

type Filters = Record<string, string>;

const dispositions = [
  "answered",
  "no_answer",
  "busy",
  "failed",
  "cancelled",
  "rejected_auth",
  "rejected_balance",
  "rejected_route",
];

function todayRange() {
  const now = new Date();
  const from = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  return { from: from.toISOString().slice(0, 16), to: "" };
}

export function CDRTable({ fixed = {}, showFilters = true }: { fixed?: Filters; showFilters?: boolean }) {
  const [params, setParams] = useSearchParams();
  const initial: Filters = { ...todayRange() };
  for (const [k, v] of params.entries()) initial[k] = v;
  const [filters, setFilters] = useState<Filters>(initial);
  const [page, setPage] = useState(1);
  const [sorting, setSorting] = useState<SortingState>([{ id: "start_time", desc: true }]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const query = useMemo(() => {
    const q: Record<string, string> = {};
    for (const [k, v] of Object.entries({ ...filters, ...fixed })) if (v) q[k] = v;
    if (q.from && q.from.length === 16) q.from = new Date(q.from + ":00Z").toISOString();
    if (q.to && q.to.length === 16) q.to = new Date(q.to + ":00Z").toISOString();
    return q;
  }, [filters, fixed]);
  const sort = sorting[0] ? `${sorting[0].desc ? "-" : ""}${sorting[0].id}` : "-start_time";
  const q = useQuery({
    queryKey: ["cdrs", query, page, sort],
    queryFn: () => get<Listing<CDR>>("/cdrs", { ...query, page, per_page: 50, sort }),
    placeholderData: (prev) => prev,
  });
  const set = (k: string, v: string) => {
    setFilters((f) => ({ ...f, [k]: v }));
    setPage(1);
  };
  const columns = useMemo<ColumnDef<CDR, unknown>[]>(
    () => [
      {
        id: "start_time",
        header: "Start",
        accessorKey: "start_time",
        cell: ({ row }) => <span className="whitespace-nowrap text-xs">{dt(row.original.start_time)}</span>,
        enableSorting: true,
      },
      { header: "Customer", accessorKey: "customer_name", enableSorting: false },
      {
        header: "Caller",
        accessorKey: "caller_number",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.caller_number}</span>,
        enableSorting: false,
      },
      {
        header: "Called",
        accessorKey: "called_number",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.called_number}</span>,
        enableSorting: false,
      },
      {
        header: "Carrier",
        accessorKey: "carrier_name",
        cell: ({ row }) => row.original.carrier_name ?? "",
        enableSorting: false,
      },
      {
        header: "Result",
        accessorKey: "disposition",
        cell: ({ row }) => (
          <span className="flex items-center gap-1">
            <Badge variant={stateVariant(row.original.disposition)}>{row.original.disposition}</Badge>
            <span className="text-xs text-muted-foreground">
              {row.original.sip_final_code} {row.original.sip_final_reason}
            </span>
          </span>
        ),
        enableSorting: false,
      },
      {
        id: "pdd_ms",
        header: "PDD",
        accessorKey: "pdd_ms",
        cell: ({ row }) => <span className="tabular">{row.original.pdd_ms ?? ""}</span>,
        enableSorting: true,
      },
      {
        id: "billsec",
        header: "Billsec",
        accessorKey: "billsec",
        cell: ({ row }) => <span className="tabular">{row.original.billsec}</span>,
        enableSorting: true,
      },
      {
        id: "sell_price",
        header: "Price",
        accessorKey: "sell_price",
        cell: ({ row }) => <span className="tabular">{money(row.original.sell_price)}</span>,
        enableSorting: true,
      },
      {
        id: "cost",
        header: "Cost",
        accessorKey: "cost",
        cell: ({ row }) => <span className="tabular">{money(row.original.cost)}</span>,
        enableSorting: true,
      },
      {
        id: "margin",
        header: "Margin",
        accessorKey: "margin",
        cell: ({ row }) => (
          <span className={`tabular ${Number(row.original.margin) < 0 ? "text-danger" : ""}`}>
            {money(row.original.margin)}
          </span>
        ),
        enableSorting: true,
      },
      {
        header: "Hops",
        accessorKey: "failover_depth",
        cell: ({ row }) => row.original.attempts.length,
        enableSorting: false,
      },
    ],
    [],
  );
  const saveFilter = () => {
    try {
      localStorage.setItem("sbc.cdr-filter", JSON.stringify(filters));
      setParams(new URLSearchParams(Object.entries(filters).filter(([, v]) => !!v)));
      toast.success("Filter saved in this browser and in the URL");
    } catch {
      toast.error("Could not save");
    }
  };
  const loadFilter = () => {
    try {
      const saved = localStorage.getItem("sbc.cdr-filter");
      if (saved) setFilters(JSON.parse(saved) as Filters);
    } catch {
      /* ignore */
    }
  };
  return (
    <div className="space-y-3">
      {showFilters && (
        <div className="flex flex-wrap items-end gap-2 rounded-md border bg-card p-3">
          <label className="text-xs">
            From (UTC)
            <Input
              type="datetime-local"
              value={filters.from ?? ""}
              onChange={(e) => set("from", e.target.value)}
              className="w-48"
            />
          </label>
          <label className="text-xs">
            To (UTC)
            <Input
              type="datetime-local"
              value={filters.to ?? ""}
              onChange={(e) => set("to", e.target.value)}
              className="w-48"
            />
          </label>
          {!fixed.customer_id && (
            <label className="text-xs w-44">
              Customer
              <CustomerSelect
                value={filters.customer_id || null}
                onChange={(v) => set("customer_id", v ?? "")}
              />
            </label>
          )}
          {!fixed.carrier_id && (
            <label className="text-xs w-44">
              Carrier
              <CarrierSelect
                value={filters.carrier_id || null}
                onChange={(v) => set("carrier_id", v ?? "")}
              />
            </label>
          )}
          <label className="text-xs">
            Called prefix
            <Input
              value={filters.prefix ?? ""}
              onChange={(e) => set("prefix", e.target.value)}
              className="w-32"
            />
          </label>
          <label className="text-xs">
            Caller
            <Input
              value={filters.caller ?? ""}
              onChange={(e) => set("caller", e.target.value)}
              className="w-32"
            />
          </label>
          <label className="text-xs">
            Disposition
            <Select
              value={filters.disposition || "all"}
              onValueChange={(v) => set("disposition", v === "all" ? "" : v)}
            >
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">any</SelectItem>
                {dispositions.map((d) => (
                  <SelectItem key={d} value={d}>
                    {d}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>
          <label className="text-xs">
            SIP code
            <Input
              value={filters.sip_code ?? ""}
              onChange={(e) => set("sip_code", e.target.value)}
              className="w-20"
            />
          </label>
          <label className="text-xs">
            Min billsec
            <Input
              value={filters.min_billsec ?? ""}
              onChange={(e) => set("min_billsec", e.target.value)}
              className="w-20"
            />
          </label>
          <label className="text-xs">
            Max billsec
            <Input
              value={filters.max_billsec ?? ""}
              onChange={(e) => set("max_billsec", e.target.value)}
              className="w-20"
            />
          </label>
          <label className="text-xs">
            Source IP
            <Input
              value={filters.src_ip ?? ""}
              onChange={(e) => set("src_ip", e.target.value)}
              className="w-32"
            />
          </label>
          <label className="text-xs">
            Call UUID
            <Input
              value={filters.call_uuid ?? ""}
              onChange={(e) => set("call_uuid", e.target.value)}
              className="w-64 font-mono"
            />
          </label>
          <label className="flex items-center gap-1 pb-2 text-xs">
            <input
              type="checkbox"
              checked={filters.negative_margin === "true"}
              onChange={(e) => set("negative_margin", e.target.checked ? "true" : "")}
            />{" "}
            negative margin only
          </label>
          <div className="ml-auto flex gap-1">
            <Button variant="outline" size="sm" onClick={loadFilter}>
              Load saved
            </Button>
            <Button variant="outline" size="sm" onClick={saveFilter}>
              <Save /> Save filter
            </Button>
            <Button variant="outline" size="sm" onClick={() => download("/cdrs/export", "cdrs.csv", query)}>
              <Download /> CSV
            </Button>
          </div>
        </div>
      )}
      <DataTable
        columns={columns}
        data={q.data?.items ?? []}
        total={q.data?.total}
        page={page}
        perPage={50}
        onPageChange={setPage}
        sorting={sorting}
        onSortingChange={setSorting}
        loading={q.isLoading}
        rowId={(r) => r.call_uuid}
        expandedId={expanded}
        onRowClick={(r) => setExpanded((e) => (e === r.call_uuid ? null : r.call_uuid))}
        renderExpanded={(c) => <CDRDetail cdr={c} />}
        emptyText="No calls match."
      />
    </div>
  );
}

export function CDRDetail({ cdr: c }: { cdr: CDR }) {
  const stats = c.rtp_stats ?? {};
  const pick = (k: string) => {
    const v = stats[k];
    return v === undefined ? "" : String(v);
  };
  return (
    <div className="grid gap-4 text-sm lg:grid-cols-3">
      <div>
        <div className="mb-1 font-medium">Call</div>
        <KV
          items={[
            [
              "UUID",
              <span className="font-mono text-xs">
                {c.call_uuid}{" "}
                <Link to={`/cdrs/${c.call_uuid}/trace`} className="underline">
                  trace
                </Link>
              </span>,
            ],
            ["Source", `${c.src_ip ?? ""}:${c.src_port ?? ""}`],
            ["Dialled", <span className="font-mono">{c.called_number_raw}</span>],
            ["Start", dt(c.start_time)],
            ["Progress", dt(c.progress_time)],
            ["Answer", dt(c.answer_time)],
            ["End", dt(c.end_time)],
            ["Duration / billsec", `${duration(c.duration)} / ${c.billsec} s`],
            ["Hangup", `${c.hangup_cause ?? ""} (${c.sip_final_code ?? ""} ${c.sip_final_reason ?? ""})`],
            ["Reject reason", c.reject_reason ?? ""],
            ["Media", `${c.codec_in ?? "?"} to ${c.codec_out ?? "?"} (${c.media_mode ?? "n/a"})`],
            [
              "Transport",
              `${c.transport_in ?? "?"}${c.srtp_in ? "+SRTP" : ""} to ${c.transport_out ?? "?"}${c.srtp_out ? "+SRTP" : ""}${c.privacy ? ", privacy" : ""}`,
            ],
            ["Node", c.sbc_node ?? ""],
          ]}
        />
      </div>
      <div>
        <div className="mb-1 font-medium">Money</div>
        <KV
          items={[
            [
              "Sell rate",
              `${money(c.sell_rate_per_min, 6)} ${c.sell_currency ?? ""}/min, ${c.sell_destination ?? ""}`,
            ],
            ["Billed seconds (sell)", c.sell_billed_seconds],
            ["Price", `${money(c.sell_price, 6)} ${c.sell_currency ?? ""}`],
            ["Buy rate", `${money(c.buy_rate_per_min, 6)} ${c.buy_currency ?? ""}/min`],
            ["Billed seconds (buy)", c.buy_billed_seconds],
            ["Cost", `${money(c.cost, 6)} ${c.buy_currency ?? ""}`],
            [
              "Margin",
              <span className={Number(c.margin) < 0 ? "text-danger" : ""}>{money(c.margin, 6)}</span>,
            ],
            [
              "Reserved / charged / released",
              `${money(c.reserved_amount)} / ${money(c.charged_amount)} / ${money(c.released_amount)}`,
            ],
          ]}
        />
        <div className="mb-1 mt-3 font-medium">RTP (customer leg)</div>
        <KV
          items={[
            [
              "Packets in / out",
              `${pick("rtp_audio_in_packet_count")} / ${pick("rtp_audio_out_packet_count")}`,
            ],
            ["Skipped (loss)", pick("rtp_audio_in_skip_packet_count")],
            ["Jitter max variance", pick("rtp_audio_in_jitter_max_variance")],
            ["Quality %", pick("rtp_audio_in_quality_percentage")],
            ["MOS", pick("rtp_audio_in_mos")],
          ]}
        />
      </div>
      <div>
        <div className="mb-1 font-medium">Attempts timeline</div>
        <ol className="space-y-1 border-l pl-3">
          {c.attempts.map((a) => (
            <li key={a.seq} className="text-xs">
              <div>
                <b>
                  {a.seq}. {a.carrier_name}
                </b>{" "}
                <Badge
                  variant={
                    a.classification === "answered"
                      ? "success"
                      : a.classification === "carrier_fault"
                        ? "warning"
                        : "danger"
                  }
                >
                  {a.classification}
                </Badge>
              </div>
              <div className="text-muted-foreground">
                {dt(a.started_at)} {a.sip_code ? `${a.sip_code} ${a.reason}` : ""} {a.hangup_cause}
                {a.pdd_ms !== null ? `, PDD ${a.pdd_ms} ms` : ""}
                {a.buy_rate_per_min ? `, buy ${money(a.buy_rate_per_min, 6)}/min` : ""}
                {a.cost ? `, attempt fee ${money(a.cost, 6)}` : ""}
              </div>
            </li>
          ))}
          {c.attempts.length === 0 && (
            <li className="text-xs text-muted-foreground">No carrier was dialled.</li>
          )}
        </ol>
      </div>
    </div>
  );
}

export function CDRsPage() {
  return (
    <div>
      <PageHeader
        title="CDRs"
        description="Every attempted call, including rejections. Click a row for attempts and RTP statistics."
      />
      <CDRTable />
    </div>
  );
}
