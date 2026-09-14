import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef } from "@tanstack/react-table";
import { ApiError, del, get, post, put } from "@/api/client";
import type {
  Account,
  BlockedPrefix,
  Customer,
  CustomerIP,
  CustomerRow,
  LedgerEntry,
  Listing,
  RateGroup,
  RouteGroup,
  CDR,
} from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input, Textarea } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataTable } from "@/components/data-table";
import { Confirm } from "@/components/confirm";
import { Field } from "@/components/form";
import { ErrorBox, KV, PageHeader } from "@/components/page";
import { RateGroupSelect, RouteGroupSelect } from "@/components/selects";
import { CDRTable } from "@/pages/cdrs";
import { useAuth } from "@/hooks/use-auth";
import { dt, money } from "@/lib/utils";

const customerSchema = z.object({
  name: z.string().min(1, "Required").max(100),
  status: z.enum(["active", "suspended", "blocked"]),
  rate_group_id: z.string().nullable(),
  route_group_id: z.string().nullable(),
  max_concurrent_calls: z.number().int().min(0),
  max_cps: z.number().int().min(0),
  allowed_codecs: z.string(),
  tech_prefix: z.string(),
  default_country_code: z.string().regex(/^\d{0,4}$/, "Digits only"),
  intl_prefix: z.string().regex(/^\d{0,4}$/, "Digits only"),
  trust_pai: z.boolean(),
  blocked_prefixes_enabled: z.boolean(),
  notes: z.string(),
  currency: z.string().length(3).optional().or(z.literal("")),
});
type CustomerForm = z.infer<typeof customerSchema>;

function toForm(c?: Customer | null): CustomerForm {
  return {
    name: c?.name ?? "",
    status: c?.status ?? "active",
    rate_group_id: c?.rate_group_id ?? null,
    route_group_id: c?.route_group_id ?? null,
    max_concurrent_calls: c?.max_concurrent_calls ?? 0,
    max_cps: c?.max_cps ?? 0,
    allowed_codecs: (c?.allowed_codecs ?? ["PCMA", "PCMU", "OPUS", "G722"]).join(","),
    tech_prefix: c?.tech_prefix ?? "",
    default_country_code: c?.default_country_code ?? "",
    intl_prefix: c?.intl_prefix ?? "00",
    trust_pai: c?.trust_pai ?? false,
    blocked_prefixes_enabled: c?.blocked_prefixes_enabled ?? true,
    notes: c?.notes ?? "",
    currency: "",
  };
}

function toPayload(f: CustomerForm) {
  return {
    ...f,
    allowed_codecs: f.allowed_codecs
      .split(",")
      .map((s) => s.trim().toUpperCase())
      .filter(Boolean),
    rate_group_id: f.rate_group_id ?? "",
    route_group_id: f.route_group_id ?? "",
    currency: f.currency || undefined,
  };
}

function applyApiErrors(e: unknown, setError: (name: keyof CustomerForm, err: { message: string }) => void) {
  if (e instanceof ApiError && e.details) {
    for (const [k, v] of Object.entries(e.details))
      setError(k as keyof CustomerForm, { message: `invalid (${v})` });
  }
  toast.error(e instanceof Error ? e.message : "Request failed");
}

export function CustomerForm({
  initial,
  onSaved,
  submitLabel = "Save",
}: {
  initial?: Customer | null;
  onSaved: (c: Customer) => void;
  submitLabel?: string;
}) {
  const form = useForm<CustomerForm>({
    resolver: zodResolver(customerSchema),
    defaultValues: toForm(initial),
  });
  const { register, handleSubmit, formState, setValue, watch, setError } = form;
  const submit = handleSubmit(async (v) => {
    try {
      const c = initial
        ? await put<Customer>(`/customers/${initial.id}`, toPayload(v))
        : await post<Customer>("/customers", toPayload(v));
      toast.success(initial ? "Customer saved" : "Customer created");
      onSaved(c);
    } catch (e) {
      applyApiErrors(e, setError);
    }
  });
  return (
    <form onSubmit={submit} className="grid gap-3 sm:grid-cols-2" noValidate>
      <Field label="Name" error={formState.errors.name?.message}>
        <Input {...register("name")} data-testid="customer-name" />
      </Field>
      <Field label="Status">
        <Select
          value={watch("status")}
          onValueChange={(v) => setValue("status", v as CustomerForm["status"])}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="active">active</SelectItem>
            <SelectItem value="suspended">suspended</SelectItem>
            <SelectItem value="blocked">blocked</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <Field label="Rate group (selling deck)">
        <RateGroupSelect
          value={watch("rate_group_id")}
          onChange={(v) => setValue("rate_group_id", v)}
          testId="customer-rate-group"
        />
      </Field>
      <Field label="Route group">
        <RouteGroupSelect
          value={watch("route_group_id")}
          onChange={(v) => setValue("route_group_id", v)}
          testId="customer-route-group"
        />
      </Field>
      <Field
        label="Max concurrent calls"
        hint="0 = unlimited"
        error={formState.errors.max_concurrent_calls?.message}
      >
        <Input type="number" min={0} {...register("max_concurrent_calls", { valueAsNumber: true })} />
      </Field>
      <Field label="Max CPS" hint="0 = unlimited" error={formState.errors.max_cps?.message}>
        <Input type="number" min={0} {...register("max_cps", { valueAsNumber: true })} />
      </Field>
      <Field label="Allowed codecs" hint="comma separated, e.g. PCMA,PCMU,OPUS,G722">
        <Input {...register("allowed_codecs")} />
      </Field>
      <Field label="Tech prefix" hint="stripped from the dialled number when present">
        <Input {...register("tech_prefix")} />
      </Field>
      <Field
        label="Default country code"
        hint="prepended to national numbers"
        error={formState.errors.default_country_code?.message}
      >
        <Input {...register("default_country_code")} placeholder="44" />
      </Field>
      <Field
        label="International prefix"
        hint="dial prefix meaning +, default 00"
        error={formState.errors.intl_prefix?.message}
      >
        <Input {...register("intl_prefix")} />
      </Field>
      {!initial && (
        <Field
          label="Account currency"
          hint="3 letters, default from configuration"
          error={formState.errors.currency?.message}
        >
          <Input {...register("currency")} placeholder="USD" maxLength={3} />
        </Field>
      )}
      <div className="flex items-center gap-2 pt-5">
        <Switch
          checked={watch("blocked_prefixes_enabled")}
          onCheckedChange={(v) => setValue("blocked_prefixes_enabled", v)}
          id="blocks"
        />
        <label htmlFor="blocks" className="text-sm">
          Apply block lists
        </label>
      </div>
      <div className="flex items-center gap-2 pt-5">
        <Switch checked={watch("trust_pai")} onCheckedChange={(v) => setValue("trust_pai", v)} id="pai" />
        <label htmlFor="pai" className="text-sm">
          Trust P-Asserted-Identity (roadmap)
        </label>
      </div>
      <Field label="Notes" className="sm:col-span-2">
        <Textarea {...register("notes")} rows={2} />
      </Field>
      <div className="sm:col-span-2 flex justify-end">
        <Button type="submit" disabled={formState.isSubmitting} data-testid="customer-submit">
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

export function CustomersPage() {
  const navigate = useNavigate();
  const { can } = useAuth();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["customers", search, status, page],
    queryFn: () => get<Listing<CustomerRow>>("/customers", { search, status, page, per_page: 50 }),
  });
  const columns = useMemo<ColumnDef<CustomerRow, unknown>[]>(
    () => [
      {
        header: "Name",
        accessorKey: "name",
        cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
      },
      {
        header: "Status",
        accessorKey: "status",
        cell: ({ row }) => <Badge variant={stateVariant(row.original.status)}>{row.original.status}</Badge>,
      },
      { header: "IPs", accessorKey: "ip_count" },
      {
        header: "Available",
        accessorKey: "available",
        cell: ({ row }) => (
          <span
            className={`tabular ${Number(row.original.available ?? 0) <= 0 ? "text-danger" : Number(row.original.available ?? 0) < 5 ? "text-warning" : "text-success"}`}
          >
            {money(row.original.available)} {row.original.currency ?? ""}
          </span>
        ),
      },
      {
        header: "Balance",
        accessorKey: "balance",
        cell: ({ row }) => <span className="tabular">{money(row.original.balance)}</span>,
      },
      {
        header: "Credit",
        accessorKey: "allowed_credit",
        cell: ({ row }) => <span className="tabular">{money(row.original.allowed_credit)}</span>,
      },
      {
        header: "Limits",
        cell: ({ row }) =>
          `${row.original.max_concurrent_calls || "∞"} calls / ${row.original.max_cps || "∞"} cps`,
      },
    ],
    [],
  );
  return (
    <div>
      <PageHeader
        title="Customers"
        description="Clients identified by source IP"
        actions={
          can("write") && (
            <Dialog open={open} onOpenChange={setOpen}>
              <DialogTrigger asChild>
                <Button data-testid="new-customer">
                  <Plus /> New customer
                </Button>
              </DialogTrigger>
              <DialogContent className="max-w-2xl">
                <DialogHeader>
                  <DialogTitle>New customer</DialogTitle>
                </DialogHeader>
                <CustomerForm
                  submitLabel="Create"
                  onSaved={(c) => {
                    setOpen(false);
                    navigate(`/customers/${c.id}`);
                  }}
                />
              </DialogContent>
            </Dialog>
          )
        }
      />
      <div className="mb-3 flex flex-wrap gap-2">
        <Input
          placeholder="Search name or IP"
          value={search}
          onChange={(e) => (setSearch(e.target.value), setPage(1))}
          className="w-64"
        />
        <Select value={status || "all"} onValueChange={(v) => (setStatus(v === "all" ? "" : v), setPage(1))}>
          <SelectTrigger className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All statuses</SelectItem>
            <SelectItem value="active">active</SelectItem>
            <SelectItem value="suspended">suspended</SelectItem>
            <SelectItem value="blocked">blocked</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {q.error && <ErrorBox error={q.error} />}
      <DataTable
        columns={columns}
        data={q.data?.items ?? []}
        total={q.data?.total}
        page={page}
        perPage={50}
        onPageChange={setPage}
        loading={q.isLoading}
        onRowClick={(c) => navigate(`/customers/${c.id}`)}
      />
    </div>
  );
}

interface Detail {
  customer: Customer;
  account: Account | null;
  available: string | null;
  ips: CustomerIP[];
  rate_group: RateGroup | null;
  route_group: RouteGroup | null;
}

export function CustomerDetailPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { can } = useAuth();
  const q = useQuery({ queryKey: ["customer", id], queryFn: () => get<Detail>(`/customers/${id}`) });
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["customer", id] });
    void qc.invalidateQueries({ queryKey: ["customers"] });
  };
  if (q.error) return <ErrorBox error={q.error} />;
  const d = q.data;
  if (!d) return null;
  const c = d.customer;
  return (
    <div>
      <PageHeader
        title={c.name}
        description={`Customer since ${dt(c.created_at)}`}
        actions={
          <>
            <Badge variant={stateVariant(c.status)}>{c.status}</Badge>
            {can("write") && (
              <Confirm
                trigger={
                  <Button variant="destructive" size="sm">
                    <Trash2 /> Delete
                  </Button>
                }
                title={`Delete customer ${c.name}?`}
                description="The customer is soft deleted, its IP addresses are removed immediately, CDRs and ledger history are kept."
                typedName={c.name}
                onConfirm={async () => {
                  await del(`/customers/${id}`);
                  toast.success("Customer deleted");
                  navigate("/customers");
                }}
              />
            )}
          </>
        }
      />
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="ips">IPs</TabsTrigger>
          <TabsTrigger value="account">Account</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
          <TabsTrigger value="blocks">Blocked prefixes</TabsTrigger>
          <TabsTrigger value="cdrs">Recent CDRs</TabsTrigger>
          <TabsTrigger value="trace">Trace</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>Account</CardTitle>
              </CardHeader>
              <CardContent>
                <KV
                  items={[
                    [
                      "Available",
                      <span
                        className={
                          Number(d.available ?? 0) <= 0
                            ? "text-danger font-semibold"
                            : "text-success font-semibold"
                        }
                      >
                        {money(d.available)} {d.account?.currency}
                      </span>,
                    ],
                    ["Balance", money(d.account?.balance)],
                    ["Credit limit", money(d.account?.allowed_credit)],
                    ["Reserved (calls in progress)", money(d.account?.reserved)],
                  ]}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>Routing</CardTitle>
              </CardHeader>
              <CardContent>
                <KV
                  items={[
                    [
                      "Rate group",
                      d.rate_group ? (
                        <Link className="underline" to={`/rate-groups/${d.rate_group.id}`}>
                          {d.rate_group.name} ({d.rate_group.currency})
                        </Link>
                      ) : (
                        "none"
                      ),
                    ],
                    [
                      "Route group",
                      d.route_group ? (
                        <Link className="underline" to={`/route-groups/${d.route_group.id}`}>
                          {d.route_group.name}
                        </Link>
                      ) : (
                        "none"
                      ),
                    ],
                    ["Concurrent calls", c.max_concurrent_calls || "unlimited"],
                    ["CPS", c.max_cps || "unlimited"],
                    ["Codecs", c.allowed_codecs.join(", ")],
                    ["Tech prefix", c.tech_prefix || "none"],
                    ["Default country code", c.default_country_code || "none"],
                    ["International prefix", c.intl_prefix],
                    ["Block lists", c.blocked_prefixes_enabled ? "applied" : "bypassed"],
                    ["Authorised IPs", d.ips.length],
                  ]}
                />
                <Button asChild variant="outline" size="sm" className="mt-3">
                  <Link to={`/simulator?customer_id=${c.id}`}>Simulate a call</Link>
                </Button>
              </CardContent>
            </Card>
          </div>
        </TabsContent>
        <TabsContent value="ips">
          <IPsTab customerId={id} ips={d.ips} onChange={invalidate} />
        </TabsContent>
        <TabsContent value="account">
          <AccountTab
            ownerType="customer"
            ownerId={id}
            account={d.account}
            available={d.available}
            onChange={invalidate}
          />
        </TabsContent>
        <TabsContent value="settings">
          <Card>
            <CardContent className="pt-4">
              {can("write") ? (
                <CustomerForm initial={c} onSaved={invalidate} />
              ) : (
                <div className="text-sm text-muted-foreground">Read only.</div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="blocks">
          <BlocksTab customerId={id} />
        </TabsContent>
        <TabsContent value="cdrs">
          <CDRTable fixed={{ customer_id: id }} />
        </TabsContent>
        <TabsContent value="trace">
          <TraceTab customerId={id} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function IPsTab({
  customerId,
  ips,
  onChange,
}: {
  customerId: string;
  ips: CustomerIP[];
  onChange: () => void;
}) {
  const { can } = useAuth();
  const [cidr, setCidr] = useState("");
  const [port, setPort] = useState("");
  const [transport, setTransport] = useState("any");
  const add = useMutation({
    mutationFn: () =>
      post(`/customers/${customerId}/ips`, {
        ip_cidr: cidr.trim(),
        port: port ? Number(port) : null,
        transport,
      }),
    onSuccess: () => {
      toast.success("Address added");
      setCidr("");
      setPort("");
      onChange();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>Authorised source addresses</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {can("write") && (
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              add.mutate();
            }}
          >
            <Field label="IP or CIDR">
              <Input
                value={cidr}
                onChange={(e) => setCidr(e.target.value)}
                placeholder="203.0.113.5 or 198.51.100.0/24"
                className="w-64"
                data-testid="ip-cidr"
              />
            </Field>
            <Field label="Port (optional)">
              <Input
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="any"
                className="w-24"
              />
            </Field>
            <Field label="Transport">
              <Select value={transport} onValueChange={setTransport}>
                <SelectTrigger className="w-28">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {["any", "udp", "tcp", "tls"].map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Button type="submit" disabled={!cidr || add.isPending} data-testid="ip-add">
              <Plus /> Add
            </Button>
          </form>
        )}
        <table className="w-full text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr>
              <th className="py-1 text-left">Address</th>
              <th className="py-1 text-left">Port</th>
              <th className="py-1 text-left">Transport</th>
              <th className="py-1 text-left">Added</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {ips.map((ip) => (
              <tr key={ip.id} className="border-t">
                <td className="py-1 font-mono">{ip.ip_cidr}</td>
                <td className="py-1">{ip.port ?? "any"}</td>
                <td className="py-1">{ip.transport}</td>
                <td className="py-1 text-muted-foreground">{dt(ip.created_at)}</td>
                <td className="py-1 text-right">
                  {can("write") && (
                    <Confirm
                      trigger={
                        <Button variant="ghost" size="sm" aria-label="Remove address">
                          <Trash2 className="text-danger" />
                        </Button>
                      }
                      title={`Remove ${ip.ip_cidr}?`}
                      description="Calls from this address will be rejected with 403 IP not authorized."
                      actionLabel="Remove"
                      onConfirm={async () => {
                        await del(`/customers/${customerId}/ips/${ip.id}`);
                        onChange();
                      }}
                    />
                  )}
                </td>
              </tr>
            ))}
            {ips.length === 0 && (
              <tr>
                <td colSpan={5} className="py-4 text-center text-muted-foreground">
                  No addresses: this customer cannot place calls.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </CardContent>
    </Card>
  );
}

export function AccountTab({
  ownerType,
  ownerId,
  account,
  available,
  onChange,
}: {
  ownerType: "customer" | "carrier";
  ownerId: string;
  account: Account | null;
  available: string | null;
  onChange: () => void;
}) {
  const { can } = useAuth();
  const base = ownerType === "customer" ? `/customers/${ownerId}` : `/carriers/${ownerId}`;
  const ledger = useQuery({
    queryKey: ["ledger", ownerType, ownerId],
    queryFn: () => get<{ items: LedgerEntry[] }>(`${base}/account/ledger`, { per_page: 100 }),
  });
  const [amount, setAmount] = useState("");
  const [desc, setDesc] = useState("");
  const [credit, setCredit] = useState(account?.allowed_credit ?? "0");
  const move = useMutation({
    mutationFn: (kind: "topup" | "adjust") => post(`${base}/account/${kind}`, { amount, description: desc }),
    onSuccess: () => {
      toast.success("Posted");
      setAmount("");
      setDesc("");
      onChange();
      void ledger.refetch();
    },
    onError: (e) => toast.error(e.message),
  });
  const setCreditM = useMutation({
    mutationFn: () => put(`${base}/account/credit`, { allowed_credit: credit }),
    onSuccess: () => {
      toast.success("Credit limit saved");
      onChange();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="grid gap-4 lg:grid-cols-3">
      <Card>
        <CardHeader>
          <CardTitle>Balance</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <KV
            items={[
              [
                "Available",
                <b>
                  {money(available)} {account?.currency}
                </b>,
              ],
              ["Balance", money(account?.balance)],
              ["Credit limit", money(account?.allowed_credit)],
              ["Reserved", money(account?.reserved)],
            ]}
          />
          {can("money") && (
            <div className="space-y-2 border-t pt-3">
              <Field label="Amount" hint="decimal string, e.g. 100.00">
                <Input
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  data-testid="topup-amount"
                />
              </Field>
              <Field label="Description">
                <Input value={desc} onChange={(e) => setDesc(e.target.value)} />
              </Field>
              <div className="flex gap-2">
                <Button
                  onClick={() => move.mutate("topup")}
                  disabled={!amount || move.isPending}
                  data-testid="topup-submit"
                >
                  Top up
                </Button>
                <Button
                  variant="outline"
                  onClick={() => move.mutate("adjust")}
                  disabled={!amount || move.isPending}
                >
                  Adjust (signed)
                </Button>
              </div>
              {ownerType === "customer" && (
                <div className="flex items-end gap-2 border-t pt-3">
                  <Field label="Credit limit">
                    <Input value={credit} onChange={(e) => setCredit(e.target.value)} />
                  </Field>
                  <Button variant="outline" onClick={() => setCreditM.mutate()}>
                    Save
                  </Button>
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>Ledger (latest 100)</CardTitle>
        </CardHeader>
        <CardContent>
          <table className="w-full text-sm">
            <thead className="text-xs text-muted-foreground">
              <tr>
                <th className="py-1 text-left">When</th>
                <th className="py-1 text-left">Type</th>
                <th className="py-1 text-right">Amount</th>
                <th className="py-1 text-right">Balance after</th>
                <th className="py-1 text-left">Description</th>
              </tr>
            </thead>
            <tbody>
              {(ledger.data?.items ?? []).map((l) => (
                <tr key={l.id} className="border-t">
                  <td className="py-1 whitespace-nowrap text-muted-foreground">{dt(l.created_at)}</td>
                  <td className="py-1">
                    <Badge
                      variant={
                        l.type === "charge" || l.type === "cost"
                          ? "danger"
                          : l.type === "topup" || l.type === "refund"
                            ? "success"
                            : "secondary"
                      }
                    >
                      {l.type}
                    </Badge>
                  </td>
                  <td
                    className={`py-1 text-right tabular ${Number(l.amount) < 0 ? "text-danger" : "text-success"}`}
                  >
                    {money(l.amount)}
                  </td>
                  <td className="py-1 text-right tabular">{money(l.balance_after)}</td>
                  <td className="py-1 text-muted-foreground">
                    {l.description}
                    {l.call_uuid && (
                      <Link to={`/cdrs?call_uuid=${l.call_uuid}`} className="ml-1 underline">
                        cdr
                      </Link>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  );
}

function BlocksTab({ customerId }: { customerId: string }) {
  const { can } = useAuth();
  const q = useQuery({
    queryKey: ["blocks", customerId],
    queryFn: () => get<BlockedPrefix[]>(`/customers/${customerId}/blocked-prefixes`),
  });
  const [prefix, setPrefix] = useState("");
  const [reason, setReason] = useState("");
  const add = useMutation({
    mutationFn: () => post(`/customers/${customerId}/blocked-prefixes`, { prefix, reason }),
    onSuccess: () => {
      setPrefix("");
      setReason("");
      void q.refetch();
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>Blocked prefixes for this customer</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Calls to a blocked prefix are rejected with 403 Destination blocked. The global blacklist (System
          page) applies as well.
        </p>
        {can("write") && (
          <div className="flex flex-wrap items-end gap-2">
            <Field label="Prefix">
              <Input
                value={prefix}
                onChange={(e) => setPrefix(e.target.value.replace(/\D/g, ""))}
                className="w-40"
              />
            </Field>
            <Field label="Reason">
              <Input value={reason} onChange={(e) => setReason(e.target.value)} className="w-64" />
            </Field>
            <Button onClick={() => add.mutate()} disabled={!prefix}>
              <Plus /> Block
            </Button>
          </div>
        )}
        <ul className="divide-y text-sm">
          {(q.data ?? []).map((b) => (
            <li key={b.id} className="flex items-center justify-between py-1">
              <span>
                <span className="font-mono">{b.prefix}</span>{" "}
                <span className="text-muted-foreground">{b.reason}</span>
              </span>
              {can("write") && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => del(`/blocked-prefixes/${b.id}`).then(() => q.refetch())}
                  aria-label="Unblock"
                >
                  <Trash2 className="text-danger" />
                </Button>
              )}
            </li>
          ))}
          {q.data?.length === 0 && <li className="py-2 text-muted-foreground">Nothing blocked.</li>}
        </ul>
      </CardContent>
    </Card>
  );
}

function TraceTab({ customerId }: { customerId: string }) {
  const { can } = useAuth();
  const q = useQuery({
    queryKey: ["trace-cdrs", customerId],
    queryFn: () => get<Listing<CDR>>("/cdrs", { customer_id: customerId, per_page: 20, sort: "-start_time" }),
  });
  const ips = useQuery({
    queryKey: ["customer-ips", customerId],
    queryFn: () => get<CustomerIP[]>(`/customers/${customerId}/ips`),
  });
  const state = useQuery({
    queryKey: ["siptrace"],
    queryFn: () => get<{ enabled: boolean; scope: string; until: string }>("/system/siptrace"),
    refetchInterval: 10_000,
  });
  const [minutes, setMinutes] = useState("5");
  const [ip, setIp] = useState("");
  const [messages, setMessages] = useState<string[] | null>(null);
  const firstIp = ips.data?.[0]?.ip_cidr.split("/")[0] ?? "";
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>
            SIP trace{" "}
            {state.data?.enabled ? (
              <Badge variant="warning">
                on ({state.data.scope}) until {dt(state.data.until)}
              </Badge>
            ) : (
              <Badge variant="secondary">off</Badge>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <p className="text-xs text-muted-foreground">
            Enables Sofia SIP tracing on the ingress profile for a few minutes (all customers are traced; the
            messages below are filtered to one address).
          </p>
          <div className="flex flex-wrap items-end gap-2">
            {can("write") && (
              <>
                <Field label="Minutes">
                  <Input value={minutes} onChange={(e) => setMinutes(e.target.value)} className="w-20" />
                </Field>
                <Button
                  variant="outline"
                  onClick={() =>
                    post("/system/siptrace", { scope: "external-ingress", minutes: Number(minutes) || 5 })
                      .then(() => (toast.success("SIP trace enabled"), state.refetch()))
                      .catch((e) => toast.error(e.message))
                  }
                >
                  Enable trace
                </Button>
                <Button variant="outline" onClick={() => del("/system/siptrace").then(() => state.refetch())}>
                  Disable
                </Button>
              </>
            )}
            <Field label="Address to show">
              <Input
                value={ip || firstIp}
                onChange={(e) => setIp(e.target.value)}
                className="w-40 font-mono"
              />
            </Field>
            <Button
              onClick={() =>
                get<{ messages: string[] }>("/system/siptrace/messages", { ip: ip || firstIp, limit: 100 })
                  .then((r) => setMessages(r.messages))
                  .catch((e) => toast.error(e.message))
              }
            >
              Show messages
            </Button>
          </div>
          {messages && (
            <div className="max-h-96 space-y-2 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px]">
              {messages.map((m, i) => (
                <pre key={i} className="whitespace-pre-wrap border-b border-dashed pb-1">
                  {m}
                </pre>
              ))}
              {messages.length === 0 && (
                <div className="text-muted-foreground">No traced messages for this address in the log.</div>
              )}
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Latest calls with their decision path</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {(q.data?.items ?? []).map((c) => (
            <details key={c.call_uuid} className="rounded-md border p-2">
              <summary className="cursor-pointer">
                <span className="font-mono text-xs">{c.call_uuid}</span> {dt(c.start_time)} to{" "}
                {c.called_number} <Badge variant={stateVariant(c.disposition)}>{c.disposition}</Badge>{" "}
                {c.sip_final_code} {c.sip_final_reason}
              </summary>
              <ol className="mt-2 space-y-1 border-l pl-3 text-xs">
                <li>
                  INVITE from {c.src_ip}: caller {c.caller_number}, dialled {c.called_number_raw}, normalised{" "}
                  {c.called_number}
                </li>
                {c.reject_reason && <li className="text-danger">Rejected: {c.reject_reason}</li>}
                {c.sell_rate_per_min && (
                  <li>
                    Sell rate {c.sell_destination} {money(c.sell_rate_per_min, 6)}/min, reserved{" "}
                    {money(c.reserved_amount)}
                  </li>
                )}
                {c.attempts.map((a) => (
                  <li key={a.seq}>
                    Attempt {a.seq}: {a.carrier_name}{" "}
                    {a.sip_code ? `${a.sip_code} ${a.reason}` : a.hangup_cause} ({a.classification}
                    {a.pdd_ms !== null ? `, PDD ${a.pdd_ms} ms` : ""})
                  </li>
                ))}
                {c.answer_time && (
                  <li>
                    Answered, billsec {c.billsec} s billed as {c.sell_billed_seconds} s: price{" "}
                    {money(c.sell_price, 6)}, cost {money(c.cost, 6)}
                  </li>
                )}
                <li>
                  Hangup {c.hangup_cause}, final {c.sip_final_code} {c.sip_final_reason}, node {c.sbc_node}
                </li>
                <li>
                  <Link to={`/cdrs/${c.call_uuid}/trace`} className="underline">
                    full stitched trace (api, Lua, FreeSWITCH)
                  </Link>
                </li>
              </ol>
            </details>
          ))}
          {q.data?.items.length === 0 && <div className="text-muted-foreground">No calls yet.</div>}
        </CardContent>
      </Card>
    </div>
  );
}
