import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef } from "@tanstack/react-table";
import { ApiError, del, get, post, put } from "@/api/client";
import type { Account, Carrier, CarrierRow, CarrierStatus, Listing, RateGroup } from "@/api/types";
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
import { RateGroupSelect } from "@/components/selects";
import { AccountTab } from "@/pages/customers";
import { CDRTable } from "@/pages/cdrs";
import { HeaderRulesTab } from "@/components/header-rules";
import { useAuth } from "@/hooks/use-auth";
import { dt, money } from "@/lib/utils";

const schema = z.object({
  name: z
    .string()
    .min(1)
    .max(64)
    .regex(/^[a-z0-9_-]+$/, "lowercase letters, digits, - and _"),
  status: z.enum(["active", "disabled"]),
  rate_group_id: z.string().nullable(),
  gateway_host: z.string().min(1, "Required"),
  gateway_port: z.number().int().min(1).max(65535),
  transport: z.enum(["udp", "tcp", "tls"]),
  dni_prefix: z.string(),
  ani_prefix: z.string(),
  strip_digits: z.number().int().min(0).max(15),
  auth_username: z.string(),
  auth_password: z.string(),
  from_domain: z.string(),
  register: z.boolean(),
  allowed_codecs: z.string(),
  max_concurrent_calls: z.number().int().min(0),
  max_cps: z.number().int().min(0),
  failover_sip_codes: z.string().regex(/^[\d,\s]*$/, "comma separated codes"),
  sip_options_ping: z.boolean(),
  ignore_early_media: z.boolean(),
  charge_failed_attempts: z.boolean(),
  notes: z.string(),
  currency: z.string(),
  media_mode: z.enum(["anchor", "proxy", "bypass"]),
  dtmf_mode: z.enum(["rfc2833", "info", "inband"]),
  srtp_mode: z.enum(["off", "optional", "mandatory"]),
  privacy_mode: z.enum(["anonymize", "pass", "ignore"]),
});
type FormT = z.infer<typeof schema>;

function toForm(c?: Carrier | null): FormT {
  return {
    name: c?.name ?? "",
    status: c?.status ?? "active",
    rate_group_id: c?.rate_group_id ?? null,
    gateway_host: c?.gateway_host ?? "",
    gateway_port: c?.gateway_port ?? 5060,
    transport: (c?.transport as FormT["transport"]) ?? "udp",
    dni_prefix: c?.dni_prefix ?? "",
    ani_prefix: c?.ani_prefix ?? "",
    strip_digits: c?.strip_digits ?? 0,
    auth_username: c?.auth_username ?? "",
    auth_password: "",
    from_domain: c?.from_domain ?? "",
    register: c?.register ?? false,
    allowed_codecs: (c?.allowed_codecs ?? ["PCMA", "PCMU"]).join(","),
    max_concurrent_calls: c?.max_concurrent_calls ?? 0,
    max_cps: c?.max_cps ?? 0,
    failover_sip_codes: (c?.failover_sip_codes ?? []).join(","),
    sip_options_ping: c?.sip_options_ping ?? true,
    ignore_early_media: c?.ignore_early_media ?? false,
    charge_failed_attempts: c?.charge_failed_attempts ?? false,
    notes: c?.notes ?? "",
    currency: "",
    media_mode: c?.media_mode ?? "anchor",
    dtmf_mode: c?.dtmf_mode ?? "rfc2833",
    srtp_mode: c?.srtp_mode ?? "off",
    privacy_mode: c?.privacy_mode ?? "anonymize",
  };
}

export function CarrierForm({
  initial,
  onSaved,
  submitLabel = "Save",
}: {
  initial?: Carrier | null;
  onSaved: (c: Carrier) => void;
  submitLabel?: string;
}) {
  const form = useForm<FormT>({ resolver: zodResolver(schema), defaultValues: toForm(initial) });
  const { register, handleSubmit, formState, setValue, watch, setError } = form;
  const submit = handleSubmit(async (v) => {
    const payload = {
      ...v,
      rate_group_id: v.rate_group_id ?? "",
      auth_password: v.auth_password ? v.auth_password : null,
      allowed_codecs: v.allowed_codecs
        .split(",")
        .map((x: string) => x.trim().toUpperCase())
        .filter(Boolean),
      failover_sip_codes: v.failover_sip_codes
        .split(",")
        .map((x: string) => Number(x.trim()))
        .filter((n: number) => n > 0),
      currency: v.currency || undefined,
    };
    try {
      const c = initial
        ? await put<Carrier>(`/carriers/${initial.id}`, payload)
        : await post<Carrier>("/carriers", payload);
      toast.success(initial ? "Carrier saved" : "Carrier created");
      onSaved(c);
    } catch (e) {
      if (e instanceof ApiError && e.details)
        for (const [k, val] of Object.entries(e.details))
          setError(k as keyof FormT, { message: `invalid (${val})` });
      toast.error(e instanceof Error ? e.message : "Request failed");
    }
  });
  return (
    <form onSubmit={submit} className="grid gap-3 sm:grid-cols-2" noValidate>
      <Field label="Name" hint="also the FreeSWITCH gateway name" error={formState.errors.name?.message}>
        <Input {...register("name")} data-testid="carrier-name" disabled={!!initial} />
      </Field>
      <Field label="Status">
        <Select value={watch("status")} onValueChange={(v) => setValue("status", v as FormT["status"])}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="active">active</SelectItem>
            <SelectItem value="disabled">disabled</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <Field label="Gateway host" error={formState.errors.gateway_host?.message}>
        <Input
          {...register("gateway_host")}
          placeholder="sip.carrier.example or 203.0.113.10"
          data-testid="carrier-host"
        />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Port" error={formState.errors.gateway_port?.message}>
          <Input type="number" {...register("gateway_port", { valueAsNumber: true })} />
        </Field>
        <Field label="Transport">
          <Select
            value={watch("transport")}
            onValueChange={(v) => setValue("transport", v as FormT["transport"])}
          >
            <SelectTrigger>
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
        </Field>
      </div>
      <Field label="Rate group (buying deck)">
        <RateGroupSelect value={watch("rate_group_id")} onChange={(v) => setValue("rate_group_id", v)} />
      </Field>
      <Field label="Allowed codecs">
        <Input {...register("allowed_codecs")} />
      </Field>
      <Field label="DNI prefix" hint="prepended to the called number">
        <Input {...register("dni_prefix")} />
      </Field>
      <Field label="ANI prefix" hint="prepended to the caller number">
        <Input {...register("ani_prefix")} />
      </Field>
      <Field
        label="Strip digits"
        hint="removed from the start of the called number before the DNI prefix"
        error={formState.errors.strip_digits?.message}
      >
        <Input type="number" {...register("strip_digits", { valueAsNumber: true })} />
      </Field>
      <Field label="From domain" hint="default: the SBC address">
        <Input {...register("from_domain")} />
      </Field>
      <Field label="Auth username">
        <Input {...register("auth_username")} autoComplete="off" />
      </Field>
      <Field label="Auth password" hint={initial ? "leave empty to keep" : ""}>
        <Input type="password" {...register("auth_password")} autoComplete="new-password" />
      </Field>
      <Field label="Max concurrent calls" hint="0 = unlimited">
        <Input type="number" {...register("max_concurrent_calls", { valueAsNumber: true })} />
      </Field>
      <Field label="Max CPS" hint="0 = unlimited">
        <Input type="number" {...register("max_cps", { valueAsNumber: true })} />
      </Field>
      <Field
        label="Failover SIP codes override"
        hint="comma separated; replaces the carrier fault list for this carrier"
        error={formState.errors.failover_sip_codes?.message}
      >
        <Input {...register("failover_sip_codes")} placeholder="e.g. 403,404,503" />
      </Field>
      {!initial && (
        <Field label="Account currency">
          <Input {...register("currency")} placeholder="USD" maxLength={3} />
        </Field>
      )}
      {(
        [
          [
            "media_mode",
            "Media",
            ["anchor", "proxy", "bypass"],
            "bypass only when the customer allows it too",
          ],
          [
            "dtmf_mode",
            "DTMF to carrier",
            ["rfc2833", "info", "inband"],
            "inband: tones generated and detected in the audio",
          ],
          ["srtp_mode", "SRTP to carrier", ["off", "optional", "mandatory"], ""],
          [
            "privacy_mode",
            "Privacy calls",
            ["anonymize", "pass", "ignore"],
            "anonymize: From anonymous + Privacy: id; pass: real identity + PAI + Privacy: id",
          ],
        ] as const
      ).map(([key, label, opts, hint]) => (
        <Field key={key} label={label} hint={hint}>
          <Select value={watch(key)} onValueChange={(v) => setValue(key, v as never)}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {opts.map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      ))}
      <div className="flex items-center gap-2 pt-5">
        <Switch
          checked={watch("sip_options_ping")}
          onCheckedChange={(v) => setValue("sip_options_ping", v)}
          id="ping"
        />
        <label htmlFor="ping" className="text-sm">
          OPTIONS ping (DOWN carriers are skipped)
        </label>
      </div>
      <div className="flex items-center gap-2 pt-5">
        <Switch checked={watch("register")} onCheckedChange={(v) => setValue("register", v)} id="reg" />
        <label htmlFor="reg" className="text-sm">
          Register with the carrier
        </label>
      </div>
      <div className="flex items-center gap-2">
        <Switch
          checked={watch("ignore_early_media")}
          onCheckedChange={(v) => setValue("ignore_early_media", v)}
          id="iem"
        />
        <label htmlFor="iem" className="text-sm">
          Ignore early media
        </label>
      </div>
      <div className="flex items-center gap-2">
        <Switch
          checked={watch("charge_failed_attempts")}
          onCheckedChange={(v) => setValue("charge_failed_attempts", v)}
          id="cfa"
        />
        <label htmlFor="cfa" className="text-sm">
          Charge failed attempts (the buy rate's connect fee per unanswered attempt)
        </label>
      </div>
      <Field label="Notes" className="sm:col-span-2">
        <Textarea {...register("notes")} rows={2} />
      </Field>
      <div className="sm:col-span-2 flex justify-end">
        <Button type="submit" disabled={formState.isSubmitting} data-testid="carrier-submit">
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

export function CarriersPage() {
  const navigate = useNavigate();
  const { can } = useAuth();
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["carriers", search, page],
    queryFn: () => get<Listing<CarrierRow>>("/carriers", { search, page, per_page: 50 }),
    refetchInterval: 15_000,
  });
  const columns = useMemo<ColumnDef<CarrierRow, unknown>[]>(
    () => [
      {
        header: "Name",
        accessorKey: "name",
        cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
      },
      {
        header: "Gateway",
        cell: ({ row }) => (
          <span className="font-mono text-xs">
            {row.original.gateway_host}:{row.original.gateway_port} {row.original.transport}
          </span>
        ),
      },
      {
        header: "State",
        cell: ({ row }) => (
          <span className="flex gap-1">
            <Badge variant={stateVariant(row.original.gateway_state)}>{row.original.gateway_state}</Badge>
            {row.original.degraded && <Badge variant="warning">DEGRADED</Badge>}
            {row.original.status === "disabled" && <Badge variant="danger">disabled</Badge>}
          </span>
        ),
      },
      {
        header: "Rate group",
        accessorKey: "rate_group_name",
        cell: ({ row }) => row.original.rate_group_name ?? "",
      },
      {
        header: "Balance",
        cell: ({ row }) => (
          <span className="tabular">
            {money(row.original.balance)} {row.original.currency ?? ""}
          </span>
        ),
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
        title="Carriers"
        description="Suppliers, one FreeSWITCH gateway each"
        actions={
          can("write") && (
            <Dialog open={open} onOpenChange={setOpen}>
              <DialogTrigger asChild>
                <Button data-testid="new-carrier">
                  <Plus /> New carrier
                </Button>
              </DialogTrigger>
              <DialogContent className="max-w-3xl">
                <DialogHeader>
                  <DialogTitle>New carrier</DialogTitle>
                </DialogHeader>
                <CarrierForm
                  submitLabel="Create"
                  onSaved={(c) => (setOpen(false), navigate(`/carriers/${c.id}`))}
                />
              </DialogContent>
            </Dialog>
          )
        }
      />
      <Input
        placeholder="Search name or host"
        value={search}
        onChange={(e) => (setSearch(e.target.value), setPage(1))}
        className="mb-3 w-64"
      />
      {q.error && <ErrorBox error={q.error} />}
      <DataTable
        columns={columns}
        data={q.data?.items ?? []}
        total={q.data?.total}
        page={page}
        perPage={50}
        onPageChange={setPage}
        loading={q.isLoading}
        onRowClick={(c) => navigate(`/carriers/${c.id}`)}
      />
    </div>
  );
}

interface Detail {
  carrier: Carrier;
  account: Account | null;
  rate_group: RateGroup | null;
  status: CarrierStatus;
}

export function CarrierDetailPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { can } = useAuth();
  const q = useQuery({
    queryKey: ["carrier", id],
    queryFn: () => get<Detail>(`/carriers/${id}`),
    refetchInterval: 15_000,
  });
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["carrier", id] });
    void qc.invalidateQueries({ queryKey: ["carriers"] });
  };
  if (q.error) return <ErrorBox error={q.error} />;
  const d = q.data;
  if (!d) return null;
  const c = d.carrier;
  const st = d.status;
  return (
    <div>
      <PageHeader
        title={c.name}
        description={`${c.gateway_host}:${c.gateway_port} ${c.transport}`}
        actions={
          <>
            <Badge variant={stateVariant(st.state)}>{st.state}</Badge>
            {st.degraded && <Badge variant="warning">degraded: {st.degraded_reason}</Badge>}
            {can("write") && (
              <Confirm
                trigger={
                  <Button variant="destructive" size="sm">
                    <Trash2 /> Delete
                  </Button>
                }
                title={`Delete carrier ${c.name}?`}
                description="The carrier is removed from every route and its gateway is unloaded from FreeSWITCH."
                typedName={c.name}
                onConfirm={async () => {
                  await del(`/carriers/${id}`);
                  toast.success("Carrier deleted");
                  navigate("/carriers");
                }}
              />
            )}
          </>
        }
      />
      <Tabs defaultValue="gateway">
        <TabsList>
          <TabsTrigger value="gateway">Gateway</TabsTrigger>
          <TabsTrigger value="account">Account</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
          <TabsTrigger value="headers">Header rules</TabsTrigger>
          <TabsTrigger value="cdrs">Recent CDRs</TabsTrigger>
        </TabsList>
        <TabsContent value="gateway">
          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>Health</CardTitle>
              </CardHeader>
              <CardContent>
                <KV
                  items={[
                    ["Gateway state", <Badge variant={stateVariant(st.state)}>{st.state}</Badge>],
                    ["OPTIONS ping", st.ping_enabled ? "enabled" : "disabled"],
                    ["State since", dt(st.since)],
                    ["Calls in progress", st.calls_in_progress ?? 0],
                    ["Attempts (5 min)", st.attempts_5m ?? 0],
                    ["Answered (5 min)", st.answered_5m ?? 0],
                    ["Consecutive faults", st.consecutive_faults ?? 0],
                    ["Circuit breaker", st.degraded ? `degraded (${st.degraded_reason})` : "closed"],
                  ]}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>Configuration</CardTitle>
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
                    ["DNI prefix / strip", `${c.dni_prefix || "none"} / ${c.strip_digits}`],
                    ["ANI prefix", c.ani_prefix || "none"],
                    ["Codecs", c.allowed_codecs.join(", ")],
                    ["Limits", `${c.max_concurrent_calls || "∞"} calls, ${c.max_cps || "∞"} cps`],
                    ["Failover override", (c.failover_sip_codes ?? []).join(", ") || "default table"],
                    ["From domain", c.from_domain || "SBC address"],
                    ["Auth", c.auth_username || "none"],
                    ["Register", c.register ? "yes" : "no"],
                    ["Early media", c.ignore_early_media ? "ignored" : "passed through"],
                    [
                      "Media / DTMF / SRTP / privacy",
                      `${c.media_mode} / ${c.dtmf_mode} / ${c.srtp_mode} / ${c.privacy_mode}`,
                    ],
                  ]}
                />
              </CardContent>
            </Card>
          </div>
        </TabsContent>
        <TabsContent value="account">
          <AccountTab
            ownerType="carrier"
            ownerId={id}
            account={d.account}
            available={d.account?.balance ?? null}
            onChange={invalidate}
          />
        </TabsContent>
        <TabsContent value="settings">
          <Card>
            <CardContent className="pt-4">
              {can("write") ? (
                <CarrierForm initial={c} onSaved={invalidate} />
              ) : (
                <div className="text-sm text-muted-foreground">Read only.</div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="headers">
          <HeaderRulesTab ownerType="carrier" ownerId={id} />
        </TabsContent>
        <TabsContent value="cdrs">
          <CDRTable fixed={{ carrier_id: id }} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
