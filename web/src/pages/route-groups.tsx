import { useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, GripVertical, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef } from "@tanstack/react-table";
import { del, get, post, put } from "@/api/client";
import type { CarrierRow, Listing, Route, RouteCarrier, RouteGroup } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
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
import { Switch } from "@/components/ui/switch";
import { DataTable } from "@/components/data-table";
import { Confirm } from "@/components/confirm";
import { Field } from "@/components/form";
import { ErrorBox, PageHeader } from "@/components/page";
import { CarrierSelect, RateGroupSelect } from "@/components/selects";
import { useAuth } from "@/hooks/use-auth";
import { money } from "@/lib/utils";

function GroupForm({ initial, onSaved }: { initial?: RouteGroup; onSaved: (g: RouteGroup) => void }) {
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [lcr, setLcr] = useState(initial?.lcr_mode ?? false);
  const m = useMutation({
    mutationFn: () =>
      initial
        ? put<RouteGroup>(`/route-groups/${initial.id}`, { name, description, lcr_mode: lcr })
        : post<RouteGroup>("/route-groups", { name, description, lcr_mode: lcr }),
    onSuccess: (g) => (toast.success("Saved"), onSaved(g)),
    onError: (e) => toast.error(e.message),
  });
  return (
    <form className="space-y-3" onSubmit={(e) => (e.preventDefault(), m.mutate())}>
      <Field label="Name">
        <Input value={name} onChange={(e) => setName(e.target.value)} data-testid="route-group-name" />
      </Field>
      <Field label="Description">
        <Input value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
      <div className="flex items-center gap-2">
        <Switch checked={lcr} onCheckedChange={setLcr} id="lcr" />
        <label htmlFor="lcr" className="text-sm">
          Least cost routing (order carriers by buy rate instead of priority)
        </label>
      </div>
      <DialogFooter>
        <Button type="submit" disabled={!name || m.isPending} data-testid="route-group-submit">
          {initial ? "Save" : "Create"}
        </Button>
      </DialogFooter>
    </form>
  );
}

export function RouteGroupsPage() {
  const navigate = useNavigate();
  const { can } = useAuth();
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["route-groups", page],
    queryFn: () => get<Listing<RouteGroup>>("/route-groups", { page, per_page: 50 }),
  });
  const columns = useMemo<ColumnDef<RouteGroup, unknown>[]>(
    () => [
      {
        header: "Name",
        accessorKey: "name",
        cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
      },
      {
        header: "Mode",
        cell: ({ row }) =>
          row.original.lcr_mode ? (
            <Badge variant="info">LCR</Badge>
          ) : (
            <Badge variant="secondary">priority</Badge>
          ),
      },
      { header: "Routes", accessorKey: "route_count" },
      { header: "Customers", accessorKey: "customer_count" },
      { header: "Description", accessorKey: "description" },
    ],
    [],
  );
  return (
    <div>
      <PageHeader
        title="Route groups"
        description="Prefix routing tables with ordered carriers"
        actions={
          can("write") && (
            <Dialog open={open} onOpenChange={setOpen}>
              <DialogTrigger asChild>
                <Button data-testid="new-route-group">
                  <Plus /> New route group
                </Button>
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>New route group</DialogTitle>
                </DialogHeader>
                <GroupForm onSaved={(g) => (setOpen(false), navigate(`/route-groups/${g.id}`))} />
              </DialogContent>
            </Dialog>
          )
        }
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
        onRowClick={(g) => navigate(`/route-groups/${g.id}`)}
      />
    </div>
  );
}

interface PreviewCarrier extends RouteCarrier {
  buy_rate_per_min: string | null;
  buy_destination: string;
  margin_per_min: string | null;
  gateway_state: string;
}

/** Route drawer: prefix, destination, and the ordered carrier list with drag and drop. */
function RouteEditor({ groupId, route, onSaved }: { groupId: string; route?: Route; onSaved: () => void }) {
  const [prefix, setPrefix] = useState(route?.prefix ?? "");
  const [destination, setDestination] = useState(route?.destination ?? "");
  const [enabled, setEnabled] = useState(route?.enabled ?? true);
  const [carriers, setCarriers] = useState<RouteCarrier[]>(route?.carriers ?? []);
  const [addId, setAddId] = useState<string | null>(null);
  const [sample, setSample] = useState("");
  const [sellGroup, setSellGroup] = useState<string | null>(null);
  const [drag, setDrag] = useState<number | null>(null);
  const allCarriers = useQuery({
    queryKey: ["carriers", "all"],
    queryFn: () => get<Listing<CarrierRow>>("/carriers", { per_page: 1000 }),
  });
  const nameOf = (id: string) => allCarriers.data?.items.find((c) => c.id === id)?.name ?? id;
  const preview = useQuery({
    queryKey: ["route-preview", route?.id, sample, sellGroup],
    queryFn: () =>
      get<{ carriers: PreviewCarrier[]; sell_rate_per_min: string | null }>(`/routes/${route!.id}/preview`, {
        number: sample,
        sell_rate_group_id: sellGroup ?? "",
      }),
    enabled: !!route,
  });
  const previewBy = new Map((preview.data?.carriers ?? []).map((c) => [c.carrier_id, c]));
  const move = (from: number, to: number) => {
    if (to < 0 || to >= carriers.length) return;
    setCarriers((cs) => {
      const c = [...cs];
      const [x] = c.splice(from, 1);
      c.splice(to, 0, x);
      return c.map((rc, i) => ({ ...rc, priority: i + 1 }));
    });
  };
  const save = useMutation({
    mutationFn: async () => {
      const body = {
        prefix,
        destination,
        enabled,
        carriers: carriers.map((c, i) => ({
          carrier_id: c.carrier_id,
          priority: i + 1,
          weight: c.weight || 100,
          enabled: c.enabled,
          window: c.window ?? "",
        })),
      };
      return route ? put(`/routes/${route.id}`, body) : post(`/route-groups/${groupId}/routes`, body);
    },
    onSuccess: () => (toast.success("Route saved"), onSaved()),
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label="Prefix" hint="digits; empty = default route">
          <Input
            value={prefix}
            onChange={(e) => setPrefix(e.target.value.replace(/\D/g, ""))}
            className="font-mono"
            data-testid="route-prefix"
          />
        </Field>
        <Field label="Destination label">
          <Input
            value={destination}
            onChange={(e) => setDestination(e.target.value)}
            data-testid="route-destination"
          />
        </Field>
        <div className="flex items-center gap-2 pt-5">
          <Switch checked={enabled} onCheckedChange={setEnabled} id="renabled" />
          <label htmlFor="renabled" className="text-sm">
            Enabled
          </label>
        </div>
      </div>
      <div>
        <div className="mb-1 text-sm font-medium">Carriers in order (drag, or use the arrows)</div>
        <ul className="space-y-1">
          {carriers.map((c, i) => {
            const pv = previewBy.get(c.carrier_id);
            return (
              <li
                key={c.carrier_id}
                draggable
                onDragStart={() => setDrag(i)}
                onDragOver={(e) => e.preventDefault()}
                onDrop={() => {
                  if (drag !== null) move(drag, i);
                  setDrag(null);
                }}
                className="flex flex-wrap items-center gap-2 rounded-md border bg-card p-2 text-sm"
                data-testid={`route-carrier-${i}`}
              >
                <GripVertical className="h-4 w-4 cursor-grab text-muted-foreground" />
                <Badge variant="secondary">#{i + 1}</Badge>
                <span className="font-medium">{c.carrier_name ?? nameOf(c.carrier_id)}</span>
                {pv && <Badge variant={stateVariant(pv.gateway_state)}>{pv.gateway_state}</Badge>}
                {pv?.buy_rate_per_min && (
                  <span className="text-xs text-muted-foreground">
                    buy {money(pv.buy_rate_per_min, 6)}/min {pv.buy_destination}
                    {pv.margin_per_min && (
                      <span className={Number(pv.margin_per_min) < 0 ? " text-danger" : " text-success"}>
                        {" "}
                        margin {money(pv.margin_per_min, 6)}
                      </span>
                    )}
                  </span>
                )}
                {pv && !pv.buy_rate_per_min && (
                  <span className="text-xs text-warning">no buy rate: skipped</span>
                )}
                <span className="ml-auto flex items-center gap-1">
                  <label className="text-xs text-muted-foreground">window</label>
                  <Input
                    className="h-7 w-52 text-xs"
                    value={c.window ?? ""}
                    placeholder="always (mon-fri 08:00-18:00 Europe/London)"
                    title="Time-of-day window: days hh:mm-hh:mm [zone]; clauses separated by ;"
                    onChange={(e) =>
                      setCarriers((cs) => cs.map((x, j) => (j === i ? { ...x, window: e.target.value } : x)))
                    }
                  />
                  <label className="text-xs text-muted-foreground">weight</label>
                  <Input
                    className="h-7 w-16 text-xs"
                    value={c.weight}
                    onChange={(e) =>
                      setCarriers((cs) =>
                        cs.map((x, j) => (j === i ? { ...x, weight: Number(e.target.value) || 100 } : x)),
                      )
                    }
                  />
                  <Checkbox
                    checked={c.enabled}
                    onCheckedChange={(v) =>
                      setCarriers((cs) => cs.map((x, j) => (j === i ? { ...x, enabled: v === true } : x)))
                    }
                    aria-label="enabled"
                  />
                  <Button variant="ghost" size="icon" onClick={() => move(i, i - 1)} aria-label="Move up">
                    <ArrowUp />
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => move(i, i + 1)} aria-label="Move down">
                    <ArrowDown />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => setCarriers((cs) => cs.filter((_, j) => j !== i))}
                    aria-label="Remove"
                  >
                    <Trash2 className="text-danger" />
                  </Button>
                </span>
              </li>
            );
          })}
          {carriers.length === 0 && (
            <li className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">
              No carriers: calls get 503 No route.
            </li>
          )}
        </ul>
        <div className="mt-2 flex items-end gap-2">
          <div className="w-64">
            <CarrierSelect value={addId} onChange={setAddId} allowNone={false} testId="route-add-carrier" />
          </div>
          <Button
            variant="outline"
            disabled={!addId || carriers.some((c) => c.carrier_id === addId)}
            onClick={() =>
              addId &&
              setCarriers((cs) => [
                ...cs,
                {
                  carrier_id: addId,
                  priority: cs.length + 1,
                  weight: 100,
                  enabled: true,
                  window: "",
                  carrier_name: undefined,
                },
              ])
            }
            data-testid="route-add-carrier-btn"
          >
            <Plus /> Add carrier
          </Button>
        </div>
      </div>
      {route && (
        <div className="flex flex-wrap items-end gap-2 rounded-md border bg-muted/30 p-2">
          <Field label="Sample number for buy rates">
            <Input
              value={sample}
              onChange={(e) => setSample(e.target.value.replace(/\D/g, ""))}
              placeholder={prefix + "..."}
              className="w-40 font-mono"
            />
          </Field>
          <Field label="Selling deck for margin">
            <div className="w-56">
              <RateGroupSelect value={sellGroup} onChange={setSellGroup} />
            </div>
          </Field>
          {preview.data?.sell_rate_per_min && (
            <span className="pb-2 text-xs text-muted-foreground">
              sell {money(preview.data.sell_rate_per_min, 6)}/min
            </span>
          )}
        </div>
      )}
      <DialogFooter>
        <Button onClick={() => save.mutate()} disabled={save.isPending} data-testid="route-save">
          {route ? "Save route" : "Create route"}
        </Button>
      </DialogFooter>
    </div>
  );
}

export function RouteGroupDetailPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { can } = useAuth();
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<Route | null | "new">(null);
  const group = useQuery({
    queryKey: ["route-group", id],
    queryFn: () => get<RouteGroup>(`/route-groups/${id}`),
  });
  const routes = useQuery({
    queryKey: ["routes", id, search],
    queryFn: () => get<Route[]>(`/route-groups/${id}/routes`, { search }),
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["routes", id] });
    void qc.invalidateQueries({ queryKey: ["route-group", id] });
    setEditing(null);
  };
  const columns = useMemo<ColumnDef<Route, unknown>[]>(
    () => [
      {
        header: "Prefix",
        accessorKey: "prefix",
        cell: ({ row }) => <span className="font-mono">{row.original.prefix || "(default)"}</span>,
      },
      { header: "Destination", accessorKey: "destination" },
      {
        header: "Carriers",
        cell: ({ row }) => (
          <span className="flex flex-wrap gap-1">
            {row.original.carriers.map((c, i) => (
              <Badge key={c.carrier_id} variant={c.enabled ? "secondary" : "outline"}>
                {i + 1}. {c.carrier_name}
              </Badge>
            ))}
            {row.original.carriers.length === 0 && <span className="text-xs text-warning">none</span>}
          </span>
        ),
      },
      {
        header: "Enabled",
        cell: ({ row }) =>
          row.original.enabled ? <Badge variant="success">yes</Badge> : <Badge variant="danger">no</Badge>,
      },
    ],
    [],
  );
  const g = group.data;
  return (
    <div>
      <PageHeader
        title={g?.name ?? "Route group"}
        description={g ? `${g.lcr_mode ? "Least cost routing" : "Priority routing"}. ${g.description}` : ""}
        actions={
          can("write") && (
            <>
              <Dialog>
                <DialogTrigger asChild>
                  <Button variant="outline">Edit group</Button>
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>Edit route group</DialogTitle>
                  </DialogHeader>
                  {g && <GroupForm initial={g} onSaved={refresh} />}
                </DialogContent>
              </Dialog>
              <Button onClick={() => setEditing("new")} data-testid="new-route">
                <Plus /> New route
              </Button>
              <Confirm
                trigger={
                  <Button variant="destructive" size="sm">
                    <Trash2 /> Delete group
                  </Button>
                }
                title={`Delete route group ${g?.name}?`}
                onConfirm={async () => {
                  try {
                    await del(`/route-groups/${id}`);
                    navigate("/route-groups");
                  } catch (e) {
                    toast.error(e instanceof Error ? e.message : "Delete failed");
                  }
                }}
              />
            </>
          )
        }
      />
      <Input
        placeholder="Filter prefix or destination"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="mb-3 w-64"
      />
      {routes.error && <ErrorBox error={routes.error} />}
      <DataTable
        columns={columns}
        data={routes.data ?? []}
        loading={routes.isLoading}
        onRowClick={(r) => can("write") && setEditing(r)}
      />
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent side="right">
          <DialogHeader>
            <DialogTitle>
              {editing === "new" ? "New route" : `Route ${(editing as Route)?.prefix || "(default)"}`}
            </DialogTitle>
          </DialogHeader>
          {editing !== null && (
            <RouteEditor groupId={id} route={editing === "new" ? undefined : editing} onSaved={refresh} />
          )}
          {editing !== "new" && editing && (
            <Card className="mt-2">
              <CardHeader>
                <CardTitle>Danger zone</CardTitle>
              </CardHeader>
              <CardContent>
                <Confirm
                  trigger={
                    <Button variant="destructive" size="sm">
                      <Trash2 /> Delete route
                    </Button>
                  }
                  title="Delete this route?"
                  onConfirm={async () => {
                    await del(`/routes/${editing.id}`);
                    refresh();
                  }}
                />
              </CardContent>
            </Card>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
