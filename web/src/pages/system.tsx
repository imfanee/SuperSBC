import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, KeyRound } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef } from "@tanstack/react-table";
import { del, get, post, put } from "@/api/client";
import type { APIKey, AuditEntry, BlockedPrefix, FXRate, Listing, User } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DataTable } from "@/components/data-table";
import { Confirm } from "@/components/confirm";
import { Field } from "@/components/form";
import { ErrorBox, KV, PageHeader } from "@/components/page";
import { useAuth } from "@/hooks/use-auth";
import { dt } from "@/lib/utils";

interface Status {
  ready: { ready: boolean; version: string; node: string; checks: Record<string, string> };
  version: string;
  node: string;
  config: Record<string, unknown>;
}
interface FailoverRules {
  number_fault: { sip_codes: number[]; causes: string[] };
  carrier_fault: { sip_codes: number[]; causes: string[] };
  min_ring_seconds_for_no_answer: number;
  default_5xx: string;
  default_other: string;
  source: string;
  cause_to_sip: Array<{ cause: string; sip_code: number; reason: string }>;
}
interface Setting {
  key: string;
  value: unknown;
  updated_at: string;
  updated_by: string | null;
}
interface Gateway {
  name: string;
  status: string;
  since: string;
  checked_at: string;
}

export function SystemPage() {
  const { can } = useAuth();
  const status = useQuery({
    queryKey: ["system-status"],
    queryFn: () => get<Status>("/system/status"),
    refetchInterval: 10_000,
  });
  const rules = useQuery({
    queryKey: ["failover-rules"],
    queryFn: () => get<FailoverRules>("/system/failover-rules"),
  });
  const gateways = useQuery({
    queryKey: ["gateways"],
    queryFn: () => get<Record<string, Gateway>>("/system/gateways"),
    refetchInterval: 10_000,
  });
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => get<Setting[]>("/system/settings") });
  const blocks = useQuery({
    queryKey: ["blocks", "global"],
    queryFn: () => get<BlockedPrefix[]>("/blocked-prefixes"),
  });
  const fx = useQuery({ queryKey: ["fx"], queryFn: () => get<FXRate[]>("/fx-rates") });
  const notes = useQuery({
    queryKey: ["notifications"],
    queryFn: () =>
      get<Array<{ id: number; kind: string; payload: Record<string, unknown>; created_at: string }>>(
        "/system/notifications",
      ),
  });
  const qc = useQueryClient();
  const [prefix, setPrefix] = useState("");
  const [reason, setReason] = useState("");
  const [fxIn, setFxIn] = useState({ base: "USD", quote: "EUR", rate: "" });
  const [settingKey, setSettingKey] = useState("");
  const [settingVal, setSettingVal] = useState("");
  const s = status.data;
  const checks = s?.ready?.checks ?? {};
  return (
    <div>
      <PageHeader title="System" description={s ? `sbc-api ${s.version} on ${s.node}` : ""} />
      <Tabs defaultValue="health">
        <TabsList>
          <TabsTrigger value="health">Health</TabsTrigger>
          <TabsTrigger value="config">Configuration</TabsTrigger>
          <TabsTrigger value="failover">Failover rules</TabsTrigger>
          <TabsTrigger value="blocklist">Global blacklist</TabsTrigger>
          <TabsTrigger value="fx">Exchange rates</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
          <TabsTrigger value="notifications">Notifications</TabsTrigger>
        </TabsList>
        <TabsContent value="health">
          {status.error && <ErrorBox error={status.error} />}
          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>Dependencies</CardTitle>
              </CardHeader>
              <CardContent>
                <KV
                  items={Object.entries(checks).map(([k, v]) => [
                    k,
                    <Badge variant={v === "ok" ? "success" : "danger"}>{v}</Badge>,
                  ])}
                />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle>Sofia gateways (OPTIONS ping)</CardTitle>
              </CardHeader>
              <CardContent>
                <KV
                  items={Object.values(gateways.data ?? {}).map((g) => [
                    g.name,
                    <span className="flex justify-end gap-2">
                      <Badge variant={stateVariant(g.status)}>{g.status}</Badge>
                      <span className="text-xs text-muted-foreground">since {dt(g.since)}</span>
                    </span>,
                  ])}
                />
                {Object.keys(gateways.data ?? {}).length === 0 && (
                  <div className="text-sm text-muted-foreground">No gateway state yet.</div>
                )}
              </CardContent>
            </Card>
          </div>
        </TabsContent>
        <TabsContent value="config">
          <Card>
            <CardHeader>
              <CardTitle>Effective configuration (environment)</CardTitle>
            </CardHeader>
            <CardContent>
              <KV items={Object.entries(s?.config ?? {}).map(([k, v]) => [k, String(v)])} />
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="failover">
          {rules.data && (
            <div className="grid gap-4 lg:grid-cols-2">
              <Card>
                <CardHeader>
                  <CardTitle>Number fault (stop, relay to customer)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-2 text-sm">
                  <div className="flex flex-wrap gap-1">
                    {rules.data.number_fault.sip_codes.map((c) => (
                      <Badge key={c} variant="danger">
                        {c}
                      </Badge>
                    ))}
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {rules.data.number_fault.causes.map((c) => (
                      <Badge key={c} variant="outline">
                        {c}
                      </Badge>
                    ))}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    NO_ANSWER counts as a number fault only after {rules.data.min_ring_seconds_for_no_answer}{" "}
                    s of ringing. Unlisted 4xx/6xx: {rules.data.default_other}.
                  </div>
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>Carrier fault (try the next carrier)</CardTitle>
                </CardHeader>
                <CardContent className="space-y-2 text-sm">
                  <div className="flex flex-wrap gap-1">
                    {rules.data.carrier_fault.sip_codes.map((c) => (
                      <Badge key={c} variant="warning">
                        {c}
                      </Badge>
                    ))}
                  </div>
                  <div className="flex flex-wrap gap-1">
                    {rules.data.carrier_fault.causes.map((c) => (
                      <Badge key={c} variant="outline">
                        {c}
                      </Badge>
                    ))}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Unlisted 5xx: {rules.data.default_5xx}. Source: {rules.data.source}. A carrier's failover
                    override replaces this list for that carrier.
                  </div>
                </CardContent>
              </Card>
              <Card className="lg:col-span-2">
                <CardHeader>
                  <CardTitle>FreeSWITCH cause to SIP code (used when the carrier sent no response)</CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="grid gap-1 text-xs sm:grid-cols-2 lg:grid-cols-4">
                    {rules.data.cause_to_sip.map((c) => (
                      <div key={c.cause} className="flex justify-between border-b border-dashed py-0.5">
                        <span className="font-mono">{c.cause}</span>
                        <span>
                          {c.sip_code} {c.reason}
                        </span>
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
            </div>
          )}
        </TabsContent>
        <TabsContent value="blocklist">
          <Card>
            <CardHeader>
              <CardTitle>Prefixes blocked for every customer</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {can("write") && (
                <div className="flex flex-wrap items-end gap-2">
                  <Field label="Prefix">
                    <Input
                      value={prefix}
                      onChange={(e) => setPrefix(e.target.value.replace(/\D/g, ""))}
                      className="w-40 font-mono"
                    />
                  </Field>
                  <Field label="Reason">
                    <Input
                      value={reason}
                      onChange={(e) => setReason(e.target.value)}
                      className="w-64"
                      placeholder="premium rate, fraud, sanction"
                    />
                  </Field>
                  <Button
                    disabled={!prefix}
                    onClick={() =>
                      post("/blocked-prefixes", { prefix, reason })
                        .then(
                          () => (
                            setPrefix(""),
                            setReason(""),
                            qc.invalidateQueries({ queryKey: ["blocks", "global"] })
                          ),
                        )
                        .catch((e) => toast.error(e.message))
                    }
                  >
                    <Plus /> Block
                  </Button>
                </div>
              )}
              <ul className="divide-y text-sm">
                {(blocks.data ?? []).map((b) => (
                  <li key={b.id} className="flex items-center justify-between py-1">
                    <span>
                      <span className="font-mono">{b.prefix}</span>{" "}
                      <span className="text-muted-foreground">{b.reason}</span>
                    </span>
                    {can("write") && (
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label="Unblock"
                        onClick={() =>
                          del(`/blocked-prefixes/${b.id}`).then(() =>
                            qc.invalidateQueries({ queryKey: ["blocks", "global"] }),
                          )
                        }
                      >
                        <Trash2 className="text-danger" />
                      </Button>
                    )}
                  </li>
                ))}
                {blocks.data?.length === 0 && (
                  <li className="py-2 text-muted-foreground">Nothing blocked globally.</li>
                )}
              </ul>
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="fx">
          <Card>
            <CardHeader>
              <CardTitle>Exchange rates (1 base = rate quote)</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {can("money") && (
                <div className="flex flex-wrap items-end gap-2">
                  <Field label="Base">
                    <Input
                      value={fxIn.base}
                      onChange={(e) => setFxIn({ ...fxIn, base: e.target.value.toUpperCase() })}
                      className="w-20"
                      maxLength={3}
                    />
                  </Field>
                  <Field label="Quote">
                    <Input
                      value={fxIn.quote}
                      onChange={(e) => setFxIn({ ...fxIn, quote: e.target.value.toUpperCase() })}
                      className="w-20"
                      maxLength={3}
                    />
                  </Field>
                  <Field label="Rate">
                    <Input
                      value={fxIn.rate}
                      onChange={(e) => setFxIn({ ...fxIn, rate: e.target.value })}
                      className="w-32"
                      placeholder="0.92"
                    />
                  </Field>
                  <Button
                    disabled={!fxIn.rate}
                    onClick={() =>
                      post("/fx-rates", fxIn)
                        .then(() => qc.invalidateQueries({ queryKey: ["fx"] }))
                        .catch((e) => toast.error(e.message))
                    }
                  >
                    <Plus /> Add
                  </Button>
                </div>
              )}
              <KV
                items={(fx.data ?? []).map((r) => [
                  `${r.base}/${r.quote}`,
                  `${r.rate} (from ${dt(r.effective_from)})`,
                ])}
              />
              {fx.data?.length === 0 && (
                <div className="text-sm text-muted-foreground">
                  No rates: every account and deck must share a currency.
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="settings">
          <Card>
            <CardHeader>
              <CardTitle>Settings (free form key/value, JSON values)</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {can("admin") && (
                <div className="flex flex-wrap items-end gap-2">
                  <Field label="Key">
                    <Input
                      value={settingKey}
                      onChange={(e) => setSettingKey(e.target.value)}
                      className="w-48"
                    />
                  </Field>
                  <Field label="Value (JSON)">
                    <Input
                      value={settingVal}
                      onChange={(e) => setSettingVal(e.target.value)}
                      className="w-64"
                      placeholder='"text" or 42 or true'
                    />
                  </Field>
                  <Button
                    disabled={!settingKey}
                    onClick={() => {
                      let v: unknown = settingVal;
                      try {
                        v = JSON.parse(settingVal);
                      } catch {
                        /* keep string */
                      }
                      put("/system/settings", { values: { [settingKey]: v } })
                        .then(() => qc.invalidateQueries({ queryKey: ["settings"] }))
                        .catch((e) => toast.error(e.message));
                    }}
                  >
                    Save
                  </Button>
                </div>
              )}
              <KV
                items={(settings.data ?? []).map((x) => [
                  x.key,
                  `${JSON.stringify(x.value)} (${x.updated_by ?? ""} ${dt(x.updated_at)})`,
                ])}
              />
            </CardContent>
          </Card>
        </TabsContent>
        <TabsContent value="notifications">
          <Card>
            <CardContent className="pt-4">
              <ul className="divide-y text-sm">
                {(notes.data ?? []).map((n) => (
                  <li key={n.id} className="flex flex-wrap gap-2 py-1">
                    <span className="text-muted-foreground">{dt(n.created_at)}</span>
                    <Badge variant="warning">{n.kind}</Badge>
                    <span className="text-xs">{JSON.stringify(n.payload)}</span>
                  </li>
                ))}
                {notes.data?.length === 0 && (
                  <li className="py-2 text-muted-foreground">No notifications.</li>
                )}
              </ul>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}

export function UsersPage() {
  const qc = useQueryClient();
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<User[]>("/users") });
  const keys = useQuery({ queryKey: ["api-keys"], queryFn: () => get<APIKey[]>("/auth/api-keys") });
  const [form, setForm] = useState({ email: "", password: "", role: "viewer" });
  const [open, setOpen] = useState(false);
  const [resetLink, setResetLink] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => post("/users", form),
    onSuccess: () => (
      toast.success("User created"),
      setOpen(false),
      setForm({ email: "", password: "", role: "viewer" }),
      qc.invalidateQueries({ queryKey: ["users"] })
    ),
    onError: (e) => toast.error(e.message),
  });
  const columns = useMemo<ColumnDef<User, unknown>[]>(
    () => [
      { header: "Email", accessorKey: "email" },
      {
        header: "Role",
        cell: ({ row }) => (
          <Select
            value={row.original.role}
            onValueChange={(v) =>
              put(`/users/${row.original.id}`, {
                email: row.original.email,
                role: v,
                status: row.original.status,
              })
                .then(() => qc.invalidateQueries({ queryKey: ["users"] }))
                .catch((e) => toast.error(e.message))
            }
          >
            <SelectTrigger className="h-7 w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["admin", "operator", "viewer"].map((r) => (
                <SelectItem key={r} value={r}>
                  {r}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ),
      },
      {
        header: "Status",
        cell: ({ row }) => <Badge variant={stateVariant(row.original.status)}>{row.original.status}</Badge>,
      },
      {
        header: "2FA",
        cell: ({ row }) =>
          row.original.totp_enabled ? (
            <Badge variant="success">on</Badge>
          ) : (
            <Badge variant="secondary">off</Badge>
          ),
      },
      { header: "Last login", cell: ({ row }) => dt(row.original.last_login_at) },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => (
          <span className="flex gap-1">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                post<{ path: string }>(`/users/${row.original.id}/reset-token`)
                  .then((r) => setResetLink(window.location.origin + r.path))
                  .catch((e) => toast.error(e.message))
              }
            >
              <KeyRound /> Reset link
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                put(`/users/${row.original.id}`, {
                  email: row.original.email,
                  role: row.original.role,
                  status: row.original.status === "active" ? "disabled" : "active",
                })
                  .then(() => qc.invalidateQueries({ queryKey: ["users"] }))
                  .catch((e) => toast.error(e.message))
              }
            >
              {row.original.status === "active" ? "Disable" : "Enable"}
            </Button>
            <Confirm
              trigger={
                <Button variant="ghost" size="sm" aria-label="Delete user">
                  <Trash2 className="text-danger" />
                </Button>
              }
              title={`Delete ${row.original.email}?`}
              onConfirm={() =>
                del(`/users/${row.original.id}`).then(() => qc.invalidateQueries({ queryKey: ["users"] }))
              }
            />
          </span>
        ),
      },
    ],
    [qc],
  );
  return (
    <div>
      <PageHeader
        title="Users and API keys"
        actions={
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button>
                <Plus /> New user
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>New user</DialogTitle>
              </DialogHeader>
              <Field label="Email">
                <Input value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
              </Field>
              <Field label="Password" hint="at least 10 characters">
                <Input
                  type="password"
                  value={form.password}
                  onChange={(e) => setForm({ ...form, password: e.target.value })}
                />
              </Field>
              <Field label="Role">
                <Select value={form.role} onValueChange={(v) => setForm({ ...form, role: v })}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {["admin", "operator", "viewer"].map((r) => (
                      <SelectItem key={r} value={r}>
                        {r}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <DialogFooter>
                <Button onClick={() => create.mutate()} disabled={create.isPending}>
                  Create
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        }
      />
      {resetLink && (
        <div className="mb-3 rounded-md border border-info/40 bg-info/10 p-3 text-sm">
          Reset link (valid one hour, shown once): <code className="break-all">{resetLink}</code>
        </div>
      )}
      {users.error && <ErrorBox error={users.error} />}
      <DataTable columns={columns} data={users.data ?? []} loading={users.isLoading} />
      <h2 className="mb-2 mt-6 text-base font-semibold">API keys (all users)</h2>
      <table className="w-full text-sm">
        <thead className="text-xs text-muted-foreground">
          <tr>
            <th className="py-1 text-left">Name</th>
            <th className="py-1 text-left">Prefix</th>
            <th className="py-1 text-left">Created</th>
            <th className="py-1 text-left">Last used</th>
            <th className="py-1 text-left">Status</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {(keys.data ?? []).map((k) => (
            <tr key={k.id} className="border-t">
              <td className="py-1">{k.name}</td>
              <td className="py-1 font-mono">{k.key_prefix}...</td>
              <td className="py-1">{dt(k.created_at)}</td>
              <td className="py-1">{dt(k.last_used_at)}</td>
              <td className="py-1">
                {k.revoked_at ? (
                  <Badge variant="danger">revoked</Badge>
                ) : (
                  <Badge variant="success">active</Badge>
                )}
              </td>
              <td className="py-1 text-right">
                {!k.revoked_at && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      del(`/auth/api-keys/${k.id}`).then(() =>
                        qc.invalidateQueries({ queryKey: ["api-keys"] }),
                      )
                    }
                  >
                    Revoke
                  </Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function AuditPage() {
  const [page, setPage] = useState(1);
  const [entity, setEntity] = useState("");
  const [actor, setActor] = useState("");
  const q = useQuery({
    queryKey: ["audit", page, entity, actor],
    queryFn: () => get<Listing<AuditEntry>>("/audit-log", { page, per_page: 50, entity_type: entity, actor }),
  });
  const columns = useMemo<ColumnDef<AuditEntry, unknown>[]>(
    () => [
      {
        header: "When",
        cell: ({ row }) => <span className="whitespace-nowrap text-xs">{dt(row.original.created_at)}</span>,
      },
      {
        header: "Actor",
        accessorKey: "actor_email",
        cell: ({ row }) => row.original.actor_email ?? "system",
      },
      {
        header: "Action",
        accessorKey: "action",
        cell: ({ row }) => <Badge variant="secondary">{row.original.action}</Badge>,
      },
      { header: "Entity", cell: ({ row }) => `${row.original.entity_type} ${row.original.entity_id ?? ""}` },
      { header: "IP", accessorKey: "remote_ip" },
      {
        header: "Change",
        cell: ({ row }) => (
          <details className="text-xs">
            <summary className="cursor-pointer text-muted-foreground">diff</summary>
            <pre className="max-h-48 max-w-lg overflow-auto whitespace-pre-wrap">
              {JSON.stringify({ before: row.original.before, after: row.original.after }, null, 1)}
            </pre>
          </details>
        ),
      },
    ],
    [],
  );
  return (
    <div>
      <PageHeader
        title="Audit log"
        description="Every change made through the API, who made it and from where"
      />
      <div className="mb-3 flex gap-2">
        <Input
          placeholder="Entity type (customer, carrier, rate...)"
          value={entity}
          onChange={(e) => (setEntity(e.target.value), setPage(1))}
          className="w-64"
        />
        <Input
          placeholder="Actor email"
          value={actor}
          onChange={(e) => (setActor(e.target.value), setPage(1))}
          className="w-64"
        />
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
      />
    </div>
  );
}
