import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FileText, Plus } from "lucide-react";
import { toast } from "sonner";
import { get, post } from "@/api/client";
import type { Invoice } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/form";
import { useAuth } from "@/hooks/use-auth";
import { dt, money } from "@/lib/utils";

/** Monthly invoices of a customer or carrier (D-63). */
export function InvoicesTab({ ownerType, ownerId }: { ownerType: "customer" | "carrier"; ownerId: string }) {
  const { can } = useAuth();
  const qc = useQueryClient();
  const key = ["invoices", ownerType, ownerId];
  const q = useQuery({
    queryKey: key,
    queryFn: () => get<Invoice[]>(`/invoices?owner_type=${ownerType}&owner_id=${ownerId}`),
  });
  const [period, setPeriod] = useState(() => {
    const d = new Date();
    d.setUTCMonth(d.getUTCMonth() - 1);
    return d.toISOString().slice(0, 7);
  });
  const usage = (i: Invoice) => (ownerType === "customer" ? i.charges : i.costs);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Invoices</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-xs text-muted-foreground">
          One prepaid usage invoice per calendar month, generated automatically after the month ends (and on
          demand here). The PDF shows opening and closing balance, movements and usage by destination.
        </p>
        {can("write") && (
          <div className="flex flex-wrap items-end gap-2">
            <Field label="Period (YYYY-MM)">
              <Input value={period} onChange={(e) => setPeriod(e.target.value)} className="w-32" />
            </Field>
            <Button
              onClick={() =>
                post("/invoices/generate", { owner_type: ownerType, owner_id: ownerId, period })
                  .then(() => (toast.success("Invoice ready"), qc.invalidateQueries({ queryKey: key })))
                  .catch((e) => toast.error(e.message))
              }
              data-testid="invoice-generate"
            >
              <Plus /> Generate
            </Button>
          </div>
        )}
        <table className="w-full text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr>
              <th className="py-1 text-left">Number</th>
              <th className="py-1 text-left">Period</th>
              <th className="py-1 text-right">Calls</th>
              <th className="py-1 text-right">Usage</th>
              <th className="py-1 text-right">Closing</th>
              <th className="py-1 text-left">Issued</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {(q.data ?? []).map((i) => (
              <tr key={i.id} className="border-t">
                <td className="py-1 font-mono">{i.number}</td>
                <td className="py-1">{i.period_start.slice(0, 7)}</td>
                <td className="py-1 text-right tabular">{i.calls}</td>
                <td className="py-1 text-right tabular">{money(usage(i))}</td>
                <td className="py-1 text-right tabular">{money(i.closing)}</td>
                <td className="py-1">{dt(i.created_at)}</td>
                <td className="py-1 text-right">
                  <a
                    href={`/api/v1/invoices/${i.id}.pdf`}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-primary hover:underline"
                  >
                    <FileText className="size-4" /> PDF
                  </a>
                </td>
              </tr>
            ))}
            {q.data?.length === 0 && (
              <tr>
                <td colSpan={7} className="py-3 text-center text-muted-foreground">
                  No invoices yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </CardContent>
    </Card>
  );
}
