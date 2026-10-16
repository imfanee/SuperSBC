import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import { Plus, RefreshCw, Trash2, Upload } from "lucide-react";
import { toast } from "sonner";
import { api, del, get, post, put } from "@/api/client";
import type { AreaCode, Listing } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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
import { Switch } from "@/components/ui/switch";
import { DataTable } from "@/components/data-table";
import { Field } from "@/components/form";
import { Confirm } from "@/components/confirm";
import { ErrorBox, PageHeader } from "@/components/page";
import { useAuth } from "@/hooks/use-auth";

const empty = {
  country_code: "",
  area_code: "",
  country: "",
  description: "",
  operator: "",
  kind: "mobile",
  enabled: true,
  notes: "",
};

function AreaForm({ initial, onSaved }: { initial?: AreaCode; onSaved: () => void }) {
  const [f, setF] = useState({ ...empty, ...(initial ?? {}) });
  const m = useMutation({
    mutationFn: () => (initial ? put(`/area-codes/${initial.id}`, f) : post("/area-codes", f)),
    onSuccess: () => (toast.success("Saved"), onSaved()),
    onError: (e) => toast.error(e.message),
  });
  return (
    <form className="grid gap-3 sm:grid-cols-2" onSubmit={(e) => (e.preventDefault(), m.mutate())}>
      <Field label="Country code" hint="without + , for example 243">
        <Input
          value={f.country_code}
          onChange={(e) => setF({ ...f, country_code: e.target.value.replace(/\D/g, "") })}
          data-testid="area-cc"
        />
      </Field>
      <Field label="Area code" hint="the digits after the country code, for example 81">
        <Input
          value={f.area_code}
          onChange={(e) => setF({ ...f, area_code: e.target.value.replace(/\D/g, "") })}
          data-testid="area-ac"
        />
      </Field>
      <Field label="Description" hint="what the CDR shows: an operator, a city or a region">
        <Input
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
          data-testid="area-desc"
        />
      </Field>
      <Field label="Operator">
        <Input
          value={f.operator}
          onChange={(e) => setF({ ...f, operator: e.target.value })}
          data-testid="area-op"
        />
      </Field>
      <Field label="Country">
        <Input value={f.country} onChange={(e) => setF({ ...f, country: e.target.value })} />
      </Field>
      <Field label="Kind">
        <Select value={f.kind} onValueChange={(v) => setF({ ...f, kind: v })}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {["mobile", "fixed", "other"].map((k) => (
              <SelectItem key={k} value={k}>
                {k}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <div className="flex items-center gap-2 pt-5">
        <Switch checked={f.enabled} onCheckedChange={(v) => setF({ ...f, enabled: v })} id="ac-enabled" />
        <label htmlFor="ac-enabled" className="text-sm">
          Used for matching
        </label>
      </div>
      <Field label="Notes" className="sm:col-span-2">
        <Input value={f.notes} onChange={(e) => setF({ ...f, notes: e.target.value })} />
      </Field>
      <DialogFooter className="sm:col-span-2">
        <Button
          type="submit"
          disabled={!f.country_code || !f.description || m.isPending}
          data-testid="area-submit"
        >
          {initial ? "Save" : "Add"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Area codes: country code plus area code to a description and operator (D-76). */
export function AreaCodesPage() {
  const { can } = useAuth();
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("");
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<AreaCode | null>(null);
  const [lookup, setLookup] = useState("");
  const file = useRef<HTMLInputElement>(null);
  const q = useQuery({
    queryKey: ["area-codes", search, kind, page],
    queryFn: () => get<Listing<AreaCode>>("/area-codes", { search, kind, page, per_page: 50 }),
  });
  const match = useQuery({
    queryKey: ["area-lookup", lookup],
    queryFn: () => get<{ number: string; match: AreaCode | null }>("/area-codes/lookup", { number: lookup }),
    enabled: lookup.length >= 3,
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ["area-codes"] });
  const columns = useMemo<ColumnDef<AreaCode, unknown>[]>(
    () => [
      {
        id: "prefix",
        header: "Prefix",
        accessorKey: "prefix",
        cell: ({ row }) => <span className="font-mono">+{row.original.prefix}</span>,
      },
      {
        id: "country_code",
        header: "Country code",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.country_code}</span>,
      },
      {
        id: "area_code",
        header: "Area code",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.area_code}</span>,
      },
      { id: "description", header: "Description", accessorKey: "description" },
      { id: "operator", header: "Operator", accessorKey: "operator" },
      { id: "country", header: "Country", accessorKey: "country" },
      {
        id: "kind",
        header: "Kind",
        cell: ({ row }) => (
          <span className="flex items-center gap-1">
            <Badge variant="secondary">{row.original.kind}</Badge>
            {!row.original.enabled && <Badge variant="danger">off</Badge>}
          </span>
        ),
      },
      {
        id: "actions",
        header: "Actions",
        cell: ({ row }) =>
          can("write") && (
            <span className="flex justify-end gap-1">
              <Button variant="ghost" size="sm" onClick={() => (setEditing(row.original), setOpen(true))}>
                Edit
              </Button>
              <Confirm
                trigger={
                  <Button variant="ghost" size="icon" aria-label="Delete">
                    <Trash2 className="text-danger" />
                  </Button>
                }
                title={`Delete +${row.original.prefix}?`}
                description="Calls to this range stop being labelled; CDRs already written keep what they have."
                actionLabel="Delete"
                onConfirm={async () => {
                  await del(`/area-codes/${row.original.id}`);
                  refresh();
                }}
              />
            </span>
          ),
      },
    ],
    [can],
  );
  const upload = async (f: File) => {
    const body = new FormData();
    body.append("file", f);
    try {
      const res = await api<{ imported: number; invalid: number }>("/area-codes/import", {
        method: "POST",
        body,
      });
      toast.success(`Imported ${res.imported}${res.invalid ? `, ${res.invalid} rejected` : ""}`);
      refresh();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Import failed");
    }
  };
  return (
    <div className="space-y-4">
      <PageHeader
        title="Area codes"
        description="Country code plus area code to a description and operator. The longest matching prefix labels every call."
        actions={
          can("write") && (
            <div className="flex flex-wrap gap-2">
              <input
                ref={file}
                type="file"
                accept=".csv,text/csv"
                className="hidden"
                onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])}
                data-testid="area-file"
              />
              <Button variant="outline" size="sm" onClick={() => file.current?.click()}>
                <Upload /> Import CSV
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  post<{ updated: number }>("/area-codes/backfill")
                    .then((r) => toast.success(`${r.updated} CDRs labelled`))
                    .catch((e) => toast.error(e.message))
                }
                data-testid="area-backfill"
              >
                <RefreshCw /> Label existing CDRs
              </Button>
              <Dialog open={open} onOpenChange={(v) => (setOpen(v), v || setEditing(null))}>
                <DialogTrigger asChild>
                  <Button size="sm" data-testid="area-new">
                    <Plus /> Add
                  </Button>
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>{editing ? `Edit +${editing.prefix}` : "Add an area code"}</DialogTitle>
                  </DialogHeader>
                  <AreaForm
                    initial={editing ?? undefined}
                    onSaved={() => (setOpen(false), setEditing(null), refresh())}
                  />
                </DialogContent>
              </Dialog>
            </div>
          )
        }
      />
      <Card>
        <CardContent className="flex flex-wrap items-end gap-3 pt-4">
          <Field label="Search">
            <Input
              value={search}
              onChange={(e) => (setSearch(e.target.value), setPage(1))}
              placeholder="prefix, description, operator"
              className="w-64"
              data-testid="area-search"
            />
          </Field>
          <Field label="Kind">
            <Select value={kind || "any"} onValueChange={(v) => (setKind(v === "any" ? "" : v), setPage(1))}>
              <SelectTrigger className="w-32">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {["any", "mobile", "fixed", "other"].map((k) => (
                  <SelectItem key={k} value={k}>
                    {k}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Test a number" hint="E.164 digits, no plus">
            <Input
              value={lookup}
              onChange={(e) => setLookup(e.target.value.replace(/\D/g, ""))}
              className="w-48 font-mono"
              data-testid="area-lookup"
            />
          </Field>
          {lookup.length >= 3 && (
            <div className="pb-2 text-sm" data-testid="area-lookup-result">
              {match.data?.match ? (
                <>
                  <span className="font-mono">+{match.data.match.prefix}</span> {match.data.match.description}
                  {match.data.match.operator && (
                    <span className="text-muted-foreground"> ({match.data.match.operator})</span>
                  )}
                </>
              ) : (
                <span className="text-muted-foreground">no match</span>
              )}
            </div>
          )}
        </CardContent>
      </Card>
      {q.error && <ErrorBox error={q.error} />}
      <DataTable
        columns={columns}
        data={q.data?.items ?? []}
        total={q.data?.total}
        page={page}
        perPage={50}
        onPageChange={setPage}
        loading={q.isLoading}
        emptyText="No area codes match."
      />
      <p className="text-xs text-muted-foreground">
        CSV columns: country_code, area_code, description, operator, country, kind, enabled, notes. A row
        replaces the entry with the same country and area code, so re-importing a corrected file is safe.
      </p>
    </div>
  );
}
