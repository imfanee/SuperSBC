import { useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Plus, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef } from "@tanstack/react-table";
import { api, del, download, get, post, put } from "@/api/client";
import type { Listing, Rate, RateGroup } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { DataTable } from "@/components/data-table";
import { Confirm } from "@/components/confirm";
import { Field } from "@/components/form";
import { ErrorBox, KV, PageHeader } from "@/components/page";
import { useAuth } from "@/hooks/use-auth";
import { dt, money } from "@/lib/utils";

function GroupForm({ initial, onSaved }: { initial?: RateGroup; onSaved: (g: RateGroup) => void }) {
  const [name, setName] = useState(initial?.name ?? "");
  const [currency, setCurrency] = useState(initial?.currency ?? "USD");
  const [description, setDescription] = useState(initial?.description ?? "");
  const m = useMutation({
    mutationFn: () =>
      initial
        ? put<RateGroup>(`/rate-groups/${initial.id}`, { name, currency, description })
        : post<RateGroup>("/rate-groups", { name, currency, description }),
    onSuccess: (g) => {
      toast.success("Saved");
      onSaved(g);
    },
    onError: (e) => toast.error(e.message),
  });
  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <Field label="Name">
        <Input value={name} onChange={(e) => setName(e.target.value)} data-testid="rate-group-name" />
      </Field>
      <Field label="Currency" hint="3 letters">
        <Input value={currency} onChange={(e) => setCurrency(e.target.value.toUpperCase())} maxLength={3} />
      </Field>
      <Field label="Description">
        <Input value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
      <DialogFooter>
        <Button type="submit" disabled={!name || m.isPending} data-testid="rate-group-submit">
          {initial ? "Save" : "Create"}
        </Button>
      </DialogFooter>
    </form>
  );
}

export function RateGroupsPage() {
  const navigate = useNavigate();
  const { can } = useAuth();
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["rate-groups", search, page],
    queryFn: () => get<Listing<RateGroup>>("/rate-groups", { search, page, per_page: 50 }),
  });
  const columns = useMemo<ColumnDef<RateGroup, unknown>[]>(
    () => [
      {
        header: "Name",
        accessorKey: "name",
        cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
      },
      { header: "Currency", accessorKey: "currency" },
      { header: "Prefixes", accessorKey: "rate_count" },
      {
        header: "Used by",
        cell: ({ row }) =>
          `${row.original.customer_count ?? 0} customers, ${row.original.carrier_count ?? 0} carriers`,
      },
      { header: "Description", accessorKey: "description" },
    ],
    [],
  );
  return (
    <div>
      <PageHeader
        title="Rate groups"
        description="Selling and buying rate decks"
        actions={
          can("write") && (
            <Dialog open={open} onOpenChange={setOpen}>
              <DialogTrigger asChild>
                <Button data-testid="new-rate-group">
                  <Plus /> New rate group
                </Button>
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>New rate group</DialogTitle>
                </DialogHeader>
                <GroupForm onSaved={(g) => (setOpen(false), navigate(`/rate-groups/${g.id}`))} />
              </DialogContent>
            </Dialog>
          )
        }
      />
      <Input
        placeholder="Search"
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
        onRowClick={(g) => navigate(`/rate-groups/${g.id}`)}
      />
    </div>
  );
}

interface ImportPreview {
  total: number;
  valid: number;
  invalid: number;
  imported: number;
  dry_run: boolean;
  replace: boolean;
  errors: Array<{ line: number; error: string }>;
  sample: Array<{ line: number; rate: Rate }>;
}

function ImportWizard({ groupId, onDone }: { groupId: string; onDone: () => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [replace, setReplace] = useState(false);
  const [partial, setPartial] = useState(false);
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const run = async (dry: boolean) => {
    if (!file) return;
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const res = await api<ImportPreview>(`/rate-groups/${groupId}/rates/import`, {
        method: "POST",
        formData: fd,
        query: { dry_run: dry, replace, partial },
      });
      setPreview(res);
      if (!dry) {
        toast.success(`Imported ${res.imported} rates`);
        onDone();
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Import failed");
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        CSV with a header row. Required columns: <code>prefix</code>, <code>rate_per_min</code> (or{" "}
        <code>rate</code>). Optional: destination, connect_fee, initial_increment, subsequent_increment,
        min_duration, effective_from, effective_to, enabled. Step 1 validates without writing.
      </p>
      <Input
        type="file"
        accept=".csv,text/csv"
        onChange={(e) => (setFile(e.target.files?.[0] ?? null), setPreview(null))}
        data-testid="import-file"
      />
      <div className="flex flex-wrap gap-4 text-sm">
        <label className="flex items-center gap-2">
          <Checkbox checked={replace} onCheckedChange={(v) => setReplace(v === true)} /> Replace the whole
          deck
        </label>
        <label className="flex items-center gap-2">
          <Checkbox checked={partial} onCheckedChange={(v) => setPartial(v === true)} /> Import valid rows
          even when some are invalid
        </label>
      </div>
      <div className="flex gap-2">
        <Button
          variant="outline"
          onClick={() => run(true)}
          disabled={!file || busy}
          data-testid="import-validate"
        >
          1. Validate
        </Button>
        <Button
          onClick={() => run(false)}
          disabled={!file || busy || !preview || (preview.invalid > 0 && !partial)}
          data-testid="import-run"
        >
          2. Import
        </Button>
      </div>
      {preview && (
        <div className="space-y-2 rounded-md border p-3 text-sm">
          <div className="flex gap-3">
            <Badge variant="secondary">{preview.total} rows</Badge>
            <Badge variant="success">{preview.valid} valid</Badge>
            <Badge variant={preview.invalid ? "danger" : "secondary"}>{preview.invalid} invalid</Badge>
            {preview.imported > 0 && <Badge variant="info">{preview.imported} imported</Badge>}
          </div>
          {preview.errors.length > 0 && (
            <div className="max-h-40 overflow-auto rounded border border-danger/40 bg-danger/5 p-2 text-xs">
              {preview.errors.map((e) => (
                <div key={e.line}>
                  line {e.line}: {e.error}
                </div>
              ))}
            </div>
          )}
          {preview.sample.length > 0 && (
            <table className="w-full text-xs">
              <thead className="text-muted-foreground">
                <tr>
                  <th className="text-left">prefix</th>
                  <th className="text-left">destination</th>
                  <th className="text-right">rate</th>
                  <th className="text-right">fee</th>
                  <th className="text-right">inc</th>
                </tr>
              </thead>
              <tbody>
                {preview.sample.map((s) => (
                  <tr key={s.line}>
                    <td className="font-mono">{s.rate.prefix}</td>
                    <td>{s.rate.destination}</td>
                    <td className="text-right tabular">{money(s.rate.rate_per_min, 6)}</td>
                    <td className="text-right tabular">{money(s.rate.connect_fee, 6)}</td>
                    <td className="text-right">
                      {s.rate.initial_increment}/{s.rate.subsequent_increment}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}

interface TestResult {
  number: string;
  matched: boolean;
  rate: Rate | null;
  sql_agrees: boolean;
  price_60s?: string;
  reserve_amount?: string;
}

export function RateGroupDetailPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { can } = useAuth();
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [editing, setEditing] = useState<Record<string, Partial<Rate>>>({});
  const [importOpen, setImportOpen] = useState(false);
  const [testNumber, setTestNumber] = useState("");
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [adding, setAdding] = useState(false);
  const newRef = useRef<{
    prefix: string;
    destination: string;
    rate: string;
    fee: string;
    inc: string;
    sub: string;
  }>({ prefix: "", destination: "", rate: "", fee: "0", inc: "60", sub: "60" });
  const group = useQuery({
    queryKey: ["rate-group", id],
    queryFn: () => get<RateGroup>(`/rate-groups/${id}`),
  });
  const rates = useQuery({
    queryKey: ["rates", id, search, page],
    queryFn: () => get<Listing<Rate>>(`/rate-groups/${id}/rates`, { search, page, per_page: 100 }),
    placeholderData: (p) => p,
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["rates", id] });
    void qc.invalidateQueries({ queryKey: ["rate-group", id] });
    void qc.invalidateQueries({ queryKey: ["rate-groups"] });
  };
  const saveRow = async (r: Rate) => {
    const e = editing[r.id];
    if (!e) return;
    try {
      await put(`/rate-groups/${id}/rates/${r.id}`, {
        prefix: e.prefix ?? r.prefix,
        destination: e.destination ?? r.destination,
        rate_per_min: e.rate_per_min ?? r.rate_per_min,
        connect_fee: e.connect_fee ?? r.connect_fee,
        initial_increment: Number(e.initial_increment ?? r.initial_increment),
        subsequent_increment: Number(e.subsequent_increment ?? r.subsequent_increment),
        min_duration: Number(e.min_duration ?? r.min_duration),
        enabled: e.enabled ?? r.enabled,
      });
      setEditing((m) => {
        const c = { ...m };
        delete c[r.id];
        return c;
      });
      toast.success("Rate saved");
      refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Save failed");
    }
  };
  const edit = (r: Rate, k: keyof Rate, v: unknown) =>
    setEditing((m) => ({ ...m, [r.id]: { ...m[r.id], [k]: v } }));
  const cell = (r: Rate, k: keyof Rate, cls = "") =>
    can("write") ? (
      <Input
        className={`h-7 text-xs ${cls}`}
        value={String(editing[r.id]?.[k] ?? r[k] ?? "")}
        onChange={(e) => edit(r, k, e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && saveRow(r)}
      />
    ) : (
      <span className={cls}>{String(r[k] ?? "")}</span>
    );
  const columns = useMemo<ColumnDef<Rate, unknown>[]>(
    () => [
      {
        id: "select",
        header: "",
        size: 24,
        cell: ({ row }) => (
          <Checkbox
            checked={!!selected[row.original.id]}
            onCheckedChange={(v) => setSelected((s) => ({ ...s, [row.original.id]: v === true }))}
            aria-label="select"
          />
        ),
      },
      { header: "Prefix", cell: ({ row }) => cell(row.original, "prefix", "font-mono w-28") },
      { header: "Destination", cell: ({ row }) => cell(row.original, "destination", "w-48") },
      { header: "Rate/min", cell: ({ row }) => cell(row.original, "rate_per_min", "tabular w-24") },
      { header: "Connect fee", cell: ({ row }) => cell(row.original, "connect_fee", "tabular w-20") },
      { header: "Initial", cell: ({ row }) => cell(row.original, "initial_increment", "w-14") },
      { header: "Subseq.", cell: ({ row }) => cell(row.original, "subsequent_increment", "w-14") },
      { header: "Min dur", cell: ({ row }) => cell(row.original, "min_duration", "w-14") },
      {
        header: "Effective",
        cell: ({ row }) => (
          <span className="whitespace-nowrap text-xs text-muted-foreground">
            {dt(row.original.effective_from)}
          </span>
        ),
      },
      {
        header: "Enabled",
        cell: ({ row }) => (
          <Checkbox
            checked={(editing[row.original.id]?.enabled ?? row.original.enabled) as boolean}
            onCheckedChange={(v) => edit(row.original, "enabled", v === true)}
            disabled={!can("write")}
          />
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) =>
          editing[row.original.id] ? (
            <Button size="sm" onClick={() => saveRow(row.original)}>
              Save
            </Button>
          ) : null,
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [editing, selected, can],
  );
  const selectedIds = Object.entries(selected)
    .filter(([, v]) => v)
    .map(([k]) => k);
  const g = group.data;
  return (
    <div>
      <PageHeader
        title={g?.name ?? "Rate group"}
        description={g ? `${g.currency}, ${g.description}` : ""}
        actions={
          <>
            <Button
              variant="outline"
              onClick={() => download(`/rate-groups/${id}/rates/export`, `${g?.name ?? "rates"}.csv`)}
            >
              <Download /> Export CSV
            </Button>
            {can("write") && (
              <>
                <Dialog open={importOpen} onOpenChange={setImportOpen}>
                  <DialogTrigger asChild>
                    <Button variant="outline" data-testid="import-open">
                      <Upload /> Import CSV
                    </Button>
                  </DialogTrigger>
                  <DialogContent className="max-w-2xl">
                    <DialogHeader>
                      <DialogTitle>Import rates into {g?.name}</DialogTitle>
                    </DialogHeader>
                    <ImportWizard groupId={id} onDone={() => (setImportOpen(false), refresh())} />
                  </DialogContent>
                </Dialog>
                <Dialog>
                  <DialogTrigger asChild>
                    <Button variant="outline">Edit group</Button>
                  </DialogTrigger>
                  <DialogContent>
                    <DialogHeader>
                      <DialogTitle>Edit rate group</DialogTitle>
                    </DialogHeader>
                    {g && <GroupForm initial={g} onSaved={refresh} />}
                  </DialogContent>
                </Dialog>
                <Confirm
                  trigger={
                    <Button variant="destructive" size="sm">
                      <Trash2 /> Delete group
                    </Button>
                  }
                  title={`Delete rate group ${g?.name}?`}
                  description="Only possible when no customer or carrier uses it."
                  onConfirm={async () => {
                    try {
                      await del(`/rate-groups/${id}`);
                      navigate("/rate-groups");
                    } catch (e) {
                      toast.error(e instanceof Error ? e.message : "Delete failed");
                    }
                  }}
                />
              </>
            )}
          </>
        }
      />
      <div className="mb-3 grid gap-3 lg:grid-cols-3">
        <Card className="lg:col-span-1">
          <CardHeader>
            <CardTitle>Test a number</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            <form
              className="flex gap-2"
              onSubmit={async (e) => {
                e.preventDefault();
                try {
                  setTestResult(
                    await get<TestResult>(`/rate-groups/${id}/rates/test`, { number: testNumber }),
                  );
                } catch (err) {
                  toast.error(err instanceof Error ? err.message : "Test failed");
                }
              }}
            >
              <Input
                value={testNumber}
                onChange={(e) => setTestNumber(e.target.value.replace(/\D/g, ""))}
                placeholder="447700900123"
                className="font-mono"
                data-testid="test-number"
              />
              <Button type="submit" variant="outline" data-testid="test-submit">
                Match
              </Button>
            </form>
            {testResult && (
              <KV
                items={
                  testResult.matched && testResult.rate
                    ? [
                        [
                          "Longest prefix",
                          <span className="font-mono" data-testid="test-prefix">
                            {testResult.rate.prefix}
                          </span>,
                        ],
                        ["Destination", testResult.rate.destination],
                        [
                          "Rate",
                          `${money(testResult.rate.rate_per_min, 6)}/min + ${money(testResult.rate.connect_fee, 6)}`,
                        ],
                        [
                          "Increments",
                          `${testResult.rate.initial_increment}/${testResult.rate.subsequent_increment}, min ${testResult.rate.min_duration}`,
                        ],
                        ["One minute", money(testResult.price_60s, 6)],
                        ["Reservation", money(testResult.reserve_amount, 6)],
                        ["SQL agrees", testResult.sql_agrees ? "yes" : "NO"],
                      ]
                    : [["Result", "no rate: the call would be rejected with 404 No rate for destination"]]
                }
              />
            )}
          </CardContent>
        </Card>
        <div className="lg:col-span-2 flex flex-wrap items-end gap-2">
          <Input
            placeholder="Filter prefix or destination"
            value={search}
            onChange={(e) => (setSearch(e.target.value), setPage(1))}
            className="w-64"
          />
          {can("write") && (
            <>
              <Button variant="outline" onClick={() => setAdding((a) => !a)}>
                <Plus /> Add rate
              </Button>
              {selectedIds.length > 0 && (
                <Confirm
                  trigger={
                    <Button variant="destructive" size="sm">
                      <Trash2 /> Delete {selectedIds.length} selected
                    </Button>
                  }
                  title={`Delete ${selectedIds.length} rates?`}
                  onConfirm={async () => {
                    await post(`/rate-groups/${id}/rates/bulk-delete`, { ids: selectedIds });
                    setSelected({});
                    refresh();
                  }}
                />
              )}
            </>
          )}
          {adding && (
            <form
              className="flex w-full flex-wrap items-end gap-2 rounded-md border bg-card p-2"
              onSubmit={async (e) => {
                e.preventDefault();
                const n = newRef.current;
                try {
                  await post(`/rate-groups/${id}/rates`, {
                    prefix: n.prefix,
                    destination: n.destination,
                    rate_per_min: n.rate,
                    connect_fee: n.fee || "0",
                    initial_increment: Number(n.inc || 60),
                    subsequent_increment: Number(n.sub || 60),
                  });
                  toast.success("Rate added");
                  setAdding(false);
                  refresh();
                } catch (err) {
                  toast.error(err instanceof Error ? err.message : "Add failed");
                }
              }}
            >
              {(["prefix", "destination", "rate", "fee", "inc", "sub"] as const).map((k) => (
                <Field key={k} label={k}>
                  <Input
                    className="w-28"
                    defaultValue={newRef.current[k]}
                    onChange={(e) => (newRef.current[k] = e.target.value)}
                    data-testid={`new-rate-${k}`}
                  />
                </Field>
              ))}
              <Button type="submit" data-testid="new-rate-submit">
                Save
              </Button>
            </form>
          )}
        </div>
      </div>
      {rates.error && <ErrorBox error={rates.error} />}
      <DataTable
        columns={columns}
        data={rates.data?.items ?? []}
        total={rates.data?.total}
        page={page}
        perPage={100}
        onPageChange={setPage}
        loading={rates.isLoading}
        cardBreakpoint={false}
      />
    </div>
  );
}
