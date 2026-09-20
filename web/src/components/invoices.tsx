import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FileText, Plus, Receipt } from "lucide-react";
import { toast } from "sonner";
import { get, post } from "@/api/client";
import type { Invoice, InvoiceLedger, Payment } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Field } from "@/components/form";
import { useAuth } from "@/hooks/use-auth";
import { dt, money } from "@/lib/utils";

function statusVariant(s: string): "success" | "warning" | "danger" | "secondary" | "info" {
  switch (s) {
    case "paid":
      return "success";
    case "partial":
      return "warning";
    case "open":
      return "danger";
    case "overpaid":
      return "info";
    default:
      return "secondary";
  }
}

function periodLabel(i: Invoice) {
  const end = new Date(new Date(i.period_end).getTime() - 1000);
  if (i.kind === "monthly") return i.period_start.slice(0, 7);
  return `${i.period_start.slice(0, 10)} to ${end.toISOString().slice(0, 10)}`;
}

function lastMonth() {
  const d = new Date();
  d.setUTCMonth(d.getUTCMonth() - 1);
  return d.toISOString().slice(0, 7);
}

/** Invoice ledger of a customer or carrier: invoices (monthly or custom period), payments and their allocation (D-72). */
export function InvoicesTab({ ownerType, ownerId }: { ownerType: "customer" | "carrier"; ownerId: string }) {
  const { can } = useAuth();
  const qc = useQueryClient();
  const key = ["invoice-ledger", ownerType, ownerId];
  const q = useQuery({
    queryKey: key,
    queryFn: () => get<InvoiceLedger>(`/invoice-ledger?owner_type=${ownerType}&owner_id=${ownerId}`),
  });
  const refresh = () => qc.invalidateQueries({ queryKey: key });
  const invoices = useMemo(
    () => (q.data?.rows ?? []).filter((r) => r.kind === "invoice").map((r) => r.invoice!),
    [q.data],
  );
  const openInvoices = invoices.filter((i) => i.status === "open" || i.status === "partial");

  // Generate: month or custom range
  const [mode, setMode] = useState<"month" | "custom">("month");
  const [period, setPeriod] = useState(lastMonth);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const generate = () => {
    const body =
      mode === "month"
        ? { owner_type: ownerType, owner_id: ownerId, period }
        : { owner_type: ownerType, owner_id: ownerId, from, to };
    post("/invoices/generate", body)
      .then(() => (toast.success("Invoice ready"), refresh()))
      .catch((e) => toast.error(e.message));
  };

  // Record a payment
  const [payOpen, setPayOpen] = useState(false);
  const [pay, setPay] = useState({
    amount: "",
    received_at: new Date().toISOString().slice(0, 10),
    reference: "",
    method: "bank",
    notes: "",
  });
  const [auto, setAuto] = useState(true);
  const [topup, setTopup] = useState(ownerType === "customer");
  const [alloc, setAlloc] = useState<Record<string, string>>({});
  const allocated = Object.values(alloc).reduce((s, v) => s + (Number(v) || 0), 0);
  const record = () => {
    const allocations = Object.entries(alloc)
      .filter(([, v]) => Number(v) > 0)
      .map(([invoice_id, amount]) => ({ invoice_id, amount }));
    post("/invoice-payments", {
      owner_type: ownerType,
      owner_id: ownerId,
      ...pay,
      allocations,
      auto_allocate: auto,
      topup,
    })
      .then(() => {
        toast.success("Payment recorded");
        setPayOpen(false);
        setPay({
          amount: "",
          received_at: new Date().toISOString().slice(0, 10),
          reference: "",
          method: "bank",
          notes: "",
        });
        setAlloc({});
        refresh();
        qc.invalidateQueries({ queryKey: [ownerType, ownerId] });
      })
      .catch((e) => toast.error(e.message));
  };
  const usage = (i: Invoice) => (ownerType === "customer" ? i.charges : i.costs);
  const led = q.data;
  const payWord = ownerType === "customer" ? "received" : "paid";

  return (
    <div className="space-y-4">
      {led && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {(
            [
              ["Invoiced", led.invoiced],
              [`Payments ${payWord}`, led.received],
              ["Outstanding", led.outstanding],
              ["Unallocated payments", led.unallocated],
            ] as const
          ).map(([label, v]) => (
            <Card key={label}>
              <CardContent className="pt-4">
                <div className="text-xs text-muted-foreground">{label}</div>
                <div className="text-lg font-semibold tabular">
                  {money(v)} <span className="text-xs font-normal text-muted-foreground">{led.currency}</span>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {can("write") && (
        <Card>
          <CardHeader>
            <CardTitle>Generate an invoice</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-end gap-2">
            <Field label="Period type">
              <Select value={mode} onValueChange={(v) => setMode(v as "month" | "custom")}>
                <SelectTrigger className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="month">Calendar month</SelectItem>
                  <SelectItem value="custom">Custom dates</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            {mode === "month" ? (
              <Field label="Month (YYYY-MM)">
                <Input
                  value={period}
                  onChange={(e) => setPeriod(e.target.value)}
                  className="w-32"
                  data-testid="invoice-period"
                />
              </Field>
            ) : (
              <>
                <Field label="From (inclusive)">
                  <Input
                    type="date"
                    value={from}
                    onChange={(e) => setFrom(e.target.value)}
                    data-testid="invoice-from"
                  />
                </Field>
                <Field label="To (exclusive)">
                  <Input
                    type="date"
                    value={to}
                    onChange={(e) => setTo(e.target.value)}
                    data-testid="invoice-to"
                  />
                </Field>
              </>
            )}
            <Button
              onClick={generate}
              disabled={mode === "custom" && (!from || !to)}
              data-testid="invoice-generate"
            >
              <Plus /> Generate
            </Button>
            <p className="basis-full text-xs text-muted-foreground">
              Monthly invoices are generated automatically after each month. Periods of one account never
              overlap: a custom invoice replaces the monthly one for the days it covers.
            </p>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Invoice ledger</CardTitle>
          {can("write") && (
            <Button
              size="sm"
              variant={payOpen ? "secondary" : "default"}
              onClick={() => setPayOpen(!payOpen)}
              data-testid="payment-toggle"
            >
              <Receipt /> Record payment
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-3">
          {payOpen && (
            <div className="space-y-3 rounded-md border bg-muted/30 p-3">
              <div className="flex flex-wrap items-end gap-2">
                <Field label={`Amount ${payWord} (${led?.currency ?? ""})`}>
                  <Input
                    value={pay.amount}
                    onChange={(e) => setPay({ ...pay, amount: e.target.value })}
                    className="w-32"
                    data-testid="payment-amount"
                  />
                </Field>
                <Field label="Date">
                  <Input
                    type="date"
                    value={pay.received_at}
                    onChange={(e) => setPay({ ...pay, received_at: e.target.value })}
                  />
                </Field>
                <Field label="Reference">
                  <Input
                    value={pay.reference}
                    onChange={(e) => setPay({ ...pay, reference: e.target.value })}
                    className="w-48"
                    placeholder="bank transaction id, receipt no."
                    title="Bank transaction id or receipt number"
                    data-testid="payment-reference"
                  />
                </Field>
                <Field label="Method">
                  <Select value={pay.method} onValueChange={(v) => setPay({ ...pay, method: v })}>
                    <SelectTrigger className="w-32">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {["bank", "card", "cash", "crypto", "other"].map((m) => (
                        <SelectItem key={m} value={m}>
                          {m}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                <Field label="Notes" className="min-w-64 flex-1">
                  <Input value={pay.notes} onChange={(e) => setPay({ ...pay, notes: e.target.value })} />
                </Field>
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                <label className="flex items-center gap-2 text-sm">
                  <Switch checked={auto} onCheckedChange={setAuto} /> Allocate the remainder to open invoices,
                  oldest first
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <Switch checked={topup} onCheckedChange={setTopup} />
                  {ownerType === "customer"
                    ? "Also top up the prepaid balance (call ledger)"
                    : "Also record it in the carrier account (call ledger)"}
                </label>
              </div>
              {openInvoices.length > 0 && (
                <div>
                  <div className="mb-1 text-xs font-medium text-muted-foreground">
                    Apply to specific invoices (optional): {money(String(allocated))} of{" "}
                    {money(pay.amount || "0")} allocated by hand
                  </div>
                  <table className="w-full text-sm">
                    <tbody>
                      {openInvoices.map((i) => {
                        const due = Number(i.amount) - Number(i.paid);
                        return (
                          <tr key={i.id} className="border-t">
                            <td className="py-1 font-mono">{i.number}</td>
                            <td className="py-1 text-muted-foreground">{periodLabel(i)}</td>
                            <td className="py-1 text-right tabular">due {money(String(due))}</td>
                            <td className="py-1 pl-2">
                              <div className="flex items-center gap-1">
                                <Input
                                  className="h-7 w-28"
                                  placeholder="0.00"
                                  value={alloc[i.id] ?? ""}
                                  onChange={(e) => setAlloc({ ...alloc, [i.id]: e.target.value })}
                                  data-testid={`alloc-${i.number}`}
                                />
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="h-7 px-2"
                                  onClick={() => setAlloc({ ...alloc, [i.id]: due.toFixed(6) })}
                                >
                                  full
                                </Button>
                              </div>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="flex justify-end">
                <Button
                  onClick={record}
                  disabled={!pay.amount || Number(pay.amount) <= 0}
                  data-testid="payment-save"
                >
                  Save payment
                </Button>
              </div>
            </div>
          )}

          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground">
              <tr>
                <th className="py-1 text-left">Date</th>
                <th className="py-1 text-left">Entry</th>
                <th className="py-1 text-left">Period / details</th>
                <th className="py-1 pr-3 text-right">Invoiced</th>
                <th className="py-1 pr-4 text-right">{ownerType === "customer" ? "Received" : "Paid"}</th>
                <th className="py-1 text-left">Status / applied to</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(led?.rows ?? []).map((r) =>
                r.kind === "invoice" && r.invoice ? (
                  <InvoiceRow key={"i" + r.invoice.id} i={r.invoice} usage={usage(r.invoice)} />
                ) : r.payment ? (
                  <PaymentRow key={"p" + r.payment.id} p={r.payment} />
                ) : null,
              )}
              {led?.rows.length === 0 && (
                <tr>
                  <td colSpan={7} className="py-3 text-center text-muted-foreground">
                    No invoices or payments yet.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          <p className="text-xs text-muted-foreground">
            This ledger tracks what was invoiced and what was {payWord} against it. The call transaction
            ledger (Account tab) is separate: it holds the prepaid balance and every call charge; a payment
            recorded with the top-up switch appears there too.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

function InvoiceRow({ i, usage }: { i: Invoice; usage: string }) {
  return (
    <tr className="border-t" data-testid="ledger-invoice">
      <td className="py-1.5 align-top">{dt(i.created_at)}</td>
      <td className="py-1.5 align-top">
        <a
          href={`/api/v1/invoices/${i.id}.pdf`}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 font-mono text-primary hover:underline"
        >
          <FileText className="size-4" /> {i.number}
        </a>
        <div className="text-xs text-muted-foreground">
          {i.kind === "custom" ? "custom period" : "monthly"}
        </div>
      </td>
      <td className="py-1.5 align-top">
        <div>{periodLabel(i)}</div>
        <div className="text-xs text-muted-foreground">
          {i.calls} calls, usage {money(String(Math.abs(Number(usage))))}
        </div>
      </td>
      <td className="py-1.5 pr-3 text-right align-top tabular">{money(i.amount)}</td>
      <td className="py-1.5 pr-4 text-right align-top tabular">{Number(i.paid) ? money(i.paid) : ""}</td>
      <td className="py-1.5 align-top">
        <Badge variant={statusVariant(i.status)}>{i.status.replace("_", " ")}</Badge>
        {i.status !== "paid" && i.status !== "nothing_due" && (
          <span className="ml-2 text-xs text-muted-foreground">
            due {money(String(Number(i.amount) - Number(i.paid)))}
          </span>
        )}
      </td>
      <td />
    </tr>
  );
}

function PaymentRow({ p }: { p: Payment }) {
  return (
    <tr className="border-t bg-muted/20" data-testid="ledger-payment">
      <td className="py-1.5 align-top">{dt(p.received_at)}</td>
      <td className="py-1.5 align-top">
        <span className="inline-flex items-center gap-1">
          <Receipt className="size-4" /> Payment{" "}
          {p.method && <span className="text-xs text-muted-foreground">({p.method})</span>}
        </span>
        {p.reference && <div className="font-mono text-xs">{p.reference}</div>}
      </td>
      <td className="py-1.5 align-top text-xs text-muted-foreground">
        {p.notes}
        {p.ledger_entry_id ? (
          <div>also posted to the call ledger (entry #{p.ledger_entry_id})</div>
        ) : (
          <div>invoice ledger only</div>
        )}
      </td>
      <td />
      <td className="py-1.5 pr-4 text-right align-top tabular">{money(p.amount)}</td>
      <td className="py-1.5 align-top text-xs">
        {p.allocations.map((a) => (
          <div key={a.invoice_id}>
            <a
              href={`/api/v1/invoices/${a.invoice_id}.pdf`}
              target="_blank"
              rel="noreferrer"
              className="font-mono text-primary hover:underline"
            >
              {a.invoice_number}
            </a>{" "}
            {money(a.amount)}
          </div>
        ))}
        {Number(p.unallocated) > 0 && (
          <div className="text-muted-foreground">unallocated {money(p.unallocated)}</div>
        )}
        {p.allocations.length === 0 && Number(p.unallocated) === 0 && (
          <span className="text-muted-foreground">none</span>
        )}
      </td>
      <td />
    </tr>
  );
}
