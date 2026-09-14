import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { get } from "@/api/client";
import type { CDR } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorBox, PageHeader } from "@/components/page";
import { CDRDetail } from "@/pages/cdrs";
import { dt, money } from "@/lib/utils";

interface Trace {
  cdr: CDR;
  ledger: Array<{
    id: number;
    type: string;
    amount: string;
    balance_after: string;
    description: string;
    created_at: string;
    owner_type: string;
  }>;
  api_lines: string[];
  freeswitch_lines: string[];
}

interface Line {
  time: string;
  source: "api" | "lua" | "freeswitch";
  text: string;
}

/** Merges the three sources into one timeline ordered by timestamp. */
function stitch(t: Trace): Line[] {
  const out: Line[] = [];
  for (const raw of t.api_lines) {
    try {
      const j = JSON.parse(raw) as Record<string, unknown>;
      const rest: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(j)) {
        if (!["time", "level", "msg", "call_uuid", "service", "node", "version"].includes(k)) rest[k] = v;
      }
      out.push({
        time: String(j.time),
        source: "api",
        text: `${String(j.level)} ${String(j.msg)} ${JSON.stringify(rest)}`,
      });
    } catch {
      out.push({ time: "", source: "api", text: raw });
    }
  }
  for (const raw of t.freeswitch_lines) {
    // "2026-09-14 22:37:01.868848 62.83% [INFO] switch_cpp.cpp:1466 [sbc uuid=...] invite ..." or "<uuid> EXECUTE ..."
    const m = raw.match(/^(?:[0-9a-f-]{36} )?(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+)/);
    const time = m ? m[1].replace(" ", "T") + "Z" : "";
    const isLua = raw.includes("[sbc uuid=");
    out.push({ time, source: isLua ? "lua" : "freeswitch", text: raw.replace(/^[0-9a-f-]{36} /, "") });
  }
  return out.sort((a, b) => (a.time < b.time ? -1 : a.time > b.time ? 1 : 0));
}

export function CallTracePage() {
  const { id = "" } = useParams();
  const q = useQuery({ queryKey: ["trace", id], queryFn: () => get<Trace>(`/cdrs/${id}/trace`) });
  if (q.error) return <ErrorBox error={q.error} />;
  const t = q.data;
  if (!t) return null;
  const lines = stitch(t);
  return (
    <div>
      <PageHeader
        title="Call trace"
        description={id}
        actions={
          <>
            <Badge variant={stateVariant(t.cdr.disposition)}>{t.cdr.disposition}</Badge>
            {t.cdr.customer_id && (
              <Link className="text-sm underline" to={`/customers/${t.cdr.customer_id}`}>
                {t.cdr.customer_name}
              </Link>
            )}
          </>
        }
      />
      <Card className="mb-4">
        <CardContent className="pt-4">
          <CDRDetail cdr={t.cdr} />
        </CardContent>
      </Card>
      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Timeline: sbc-api, Lua and FreeSWITCH lines for this uuid</CardTitle>
          </CardHeader>
          <CardContent>
            <ol className="max-h-[70vh] space-y-1 overflow-auto font-mono text-[11px]">
              {lines.map((l, i) => (
                <li key={i} className="flex gap-2 border-b border-dashed py-0.5">
                  <span className="w-40 shrink-0 text-muted-foreground">{l.time ? dt(l.time) : ""}</span>
                  <Badge
                    variant={l.source === "api" ? "info" : l.source === "lua" ? "warning" : "secondary"}
                    className="h-4 shrink-0 px-1 text-[10px]"
                  >
                    {l.source}
                  </Badge>
                  <span className="break-all">{l.text}</span>
                </li>
              ))}
              {lines.length === 0 && (
                <li className="text-muted-foreground">
                  No log lines found (api lines are kept 24 h in Redis, FreeSWITCH lines come from the mounted
                  log file).
                </li>
              )}
            </ol>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Ledger entries of this call</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-1 text-xs">
              {t.ledger.map((l) => (
                <li key={l.id} className="flex justify-between border-b border-dashed py-0.5">
                  <span>
                    <Badge variant="secondary" className="mr-1">
                      {l.owner_type}
                    </Badge>
                    {l.type}
                  </span>
                  <span className={`tabular ${Number(l.amount) < 0 ? "text-danger" : "text-success"}`}>
                    {money(l.amount, 6)}
                  </span>
                </li>
              ))}
              {t.ledger.length === 0 && <li className="text-muted-foreground">No money moved.</li>}
            </ul>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
