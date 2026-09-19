import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { ChevronDown, ChevronRight, Download, FileDown, RefreshCw } from "lucide-react";
import { get } from "@/api/client";
import type { CDR } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ErrorBox, PageHeader } from "@/components/page";
import { cn } from "@/lib/utils";

interface Message {
  id: number;
  time: string;
  src_ip: string;
  src_port: number;
  dst_ip: string;
  dst_port: number;
  transport: string;
  call_id: string;
  leg: "customer" | "carrier";
  method: string;
  summary: string;
  raw: string;
}

interface Trace {
  call_uuid: string;
  customer_call_id: string;
  carrier_call_ids: string[];
  from: string;
  to: string;
  messages: Message[];
}

function fmtTime(iso: string) {
  const d = new Date(iso);
  return d.toISOString().slice(11, 23);
}

function methodVariant(m: string): "success" | "warning" | "danger" | "info" | "secondary" {
  const n = Number(m);
  if (!Number.isNaN(n)) {
    if (n < 200) return "info";
    if (n < 300) return "success";
    if (n < 400) return "warning";
    return "danger";
  }
  if (m === "INVITE") return "info";
  if (m === "BYE" || m === "CANCEL") return "warning";
  return "secondary";
}

/** Splits a raw SIP message into its header block and body. */
function splitMessage(raw: string): { headers: string; body: string } {
  const norm = raw.replace(/\r\n/g, "\n");
  const i = norm.indexOf("\n\n");
  if (i < 0) return { headers: norm, body: "" };
  return { headers: norm.slice(0, i), body: norm.slice(i + 2).trimEnd() };
}

function MessageRow({
  m,
  index,
  first,
  open,
  onToggle,
}: {
  m: Message;
  index: number;
  first: number;
  open: boolean;
  onToggle: () => void;
}) {
  const { headers, body } = splitMessage(m.raw);
  const dt = new Date(m.time).getTime() - first;
  return (
    <>
      <tr className="cursor-pointer border-t hover:bg-muted/40" onClick={onToggle} data-testid="sip-row">
        <td className="w-6 py-1.5 pl-2 text-muted-foreground">
          {open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
        </td>
        <td className="py-1.5 pr-3 font-mono text-xs tabular">
          {fmtTime(m.time)}
          <span className="ml-1 text-muted-foreground">+{(dt / 1000).toFixed(3)}s</span>
        </td>
        <td className="py-1.5 pr-3">
          <Badge variant={m.leg === "customer" ? "info" : "secondary"}>{m.leg}</Badge>
        </td>
        <td className="py-1.5 pr-3 font-mono text-xs">
          {m.src_ip}:{m.src_port} <span className="text-muted-foreground">to</span> {m.dst_ip}:{m.dst_port}
          <span className="ml-1 text-muted-foreground">{m.transport}</span>
        </td>
        <td className="py-1.5 pr-3">
          <Badge variant={methodVariant(m.method)}>{m.method}</Badge>
        </td>
        <td className="py-1.5 pr-2 font-mono text-xs">{m.summary}</td>
        <td className="py-1.5 pr-2 text-right text-xs text-muted-foreground">#{index + 1}</td>
      </tr>
      {open && (
        <tr className="border-t bg-muted/20">
          <td />
          <td colSpan={6} className="py-2 pr-2">
            <div className="grid gap-3 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
              <div>
                <div className="mb-1 text-xs font-medium text-muted-foreground">Headers</div>
                <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md border bg-background p-2 font-mono text-xs leading-snug">
                  {headers}
                </pre>
              </div>
              <div>
                <div className="mb-1 text-xs font-medium text-muted-foreground">
                  Body {body ? `(${body.length} bytes)` : "(none)"}
                </div>
                {body && (
                  <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-md border bg-background p-2 font-mono text-xs leading-snug">
                    {body}
                  </pre>
                )}
                <div className="mt-2 text-xs text-muted-foreground">
                  Call-ID <span className="break-all font-mono">{m.call_id}</span>
                </div>
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

/** Full SIP trace of one call from the HOMER capture store (D-71). Opens in its own tab from the CDR detail. */
export function SIPTracePage() {
  const { id = "" } = useParams();
  const cdr = useQuery({ queryKey: ["cdr", id], queryFn: () => get<CDR>(`/cdrs/${id}`) });
  const trace = useQuery({ queryKey: ["cdr-sip", id], queryFn: () => get<Trace>(`/cdrs/${id}/sip`) });
  const [open, setOpen] = useState<Record<number, boolean>>({});
  const [leg, setLeg] = useState<"all" | "customer" | "carrier">("all");
  const messages = useMemo(
    () => (trace.data?.messages ?? []).filter((m) => leg === "all" || m.leg === leg),
    [trace.data, leg],
  );
  const first = trace.data?.messages[0] ? new Date(trace.data.messages[0].time).getTime() : 0;
  const allOpen = messages.length > 0 && messages.every((m) => open[m.id]);
  const download = () => {
    const text = (trace.data?.messages ?? [])
      .map(
        (m) =>
          `--- ${m.time} ${m.leg} ${m.src_ip}:${m.src_port} -> ${m.dst_ip}:${m.dst_port} ${m.transport}\n${m.raw.replace(/\r\n/g, "\n")}\n`,
      )
      .join("\n");
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `sip-${id}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };
  const c = cdr.data;
  return (
    <div className="space-y-4">
      <PageHeader
        title="SIP trace"
        description={
          c
            ? `${c.customer_name ?? "unknown customer"} ${c.caller_number} to ${c.called_number}, ${c.disposition} ${c.sip_final_code ?? ""}`
            : "Loading call..."
        }
        actions={
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => trace.refetch()}>
              <RefreshCw /> Refresh
            </Button>
            <Button variant="outline" size="sm" onClick={download} disabled={!trace.data?.messages.length}>
              <Download /> Download .txt
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!trace.data?.messages.length}
                  data-testid="pcap-menu"
                >
                  <FileDown /> Export pcap
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem asChild>
                  <a href={`/api/v1/cdrs/${id}/sip.pcap?leg=customer`} download data-testid="pcap-customer">
                    Customer leg only
                  </a>
                </DropdownMenuItem>
                <DropdownMenuItem asChild>
                  <a href={`/api/v1/cdrs/${id}/sip.pcap?leg=carrier`} download data-testid="pcap-carrier">
                    Carrier leg only
                  </a>
                </DropdownMenuItem>
                <DropdownMenuItem asChild>
                  <a href={`/api/v1/cdrs/${id}/sip.pcap?leg=all`} download data-testid="pcap-all">
                    Complete call (both legs)
                  </a>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Button variant="outline" size="sm" asChild>
              <Link to={`/cdrs/${id}/trace`}>Call trace</Link>
            </Button>
          </div>
        }
      />
      {trace.error && <ErrorBox error={trace.error} />}
      {trace.data && (
        <Card>
          <CardContent className="space-y-3 pt-4">
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <span>
                {trace.data.messages.length} messages, customer Call-ID{" "}
                <span className="break-all font-mono">{trace.data.customer_call_id || "n/a"}</span>
                {trace.data.carrier_call_ids.length > 0 && (
                  <>
                    , carrier {trace.data.carrier_call_ids.length === 1 ? "Call-ID" : "Call-IDs"}{" "}
                    <span className="break-all font-mono">{trace.data.carrier_call_ids.join(", ")}</span>
                  </>
                )}
              </span>
              <span className="ml-auto flex items-center gap-1">
                {(["all", "customer", "carrier"] as const).map((l) => (
                  <Button
                    key={l}
                    size="sm"
                    variant={leg === l ? "default" : "ghost"}
                    className={cn("h-7 px-2")}
                    onClick={() => setLeg(l)}
                  >
                    {l}
                  </Button>
                ))}
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2"
                  onClick={() => {
                    const next: Record<number, boolean> = {};
                    if (!allOpen) for (const m of messages) next[m.id] = true;
                    setOpen(next);
                  }}
                >
                  {allOpen ? "Collapse all" : "Expand all"}
                </Button>
              </span>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="text-left text-xs text-muted-foreground">
                  <tr>
                    <th />
                    <th className="py-1 pr-3">Time</th>
                    <th className="py-1 pr-3">Leg</th>
                    <th className="py-1 pr-3">Source to destination</th>
                    <th className="py-1 pr-3">Method</th>
                    <th className="py-1 pr-2">First line</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {messages.map((m, i) => (
                    <MessageRow
                      key={m.id}
                      m={m}
                      index={i}
                      first={first}
                      open={!!open[m.id]}
                      onToggle={() => setOpen({ ...open, [m.id]: !open[m.id] })}
                    />
                  ))}
                  {messages.length === 0 && (
                    <tr>
                      <td colSpan={7} className="py-6 text-center text-muted-foreground">
                        No captured SIP for this call. Capture keeps messages for a limited time (retention),
                        and only calls made while HEP capture was on are stored.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
