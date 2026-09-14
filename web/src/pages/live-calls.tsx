import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { PhoneOff } from "lucide-react";
import { toast } from "sonner";
import type { ColumnDef } from "@tanstack/react-table";
import { del, get } from "@/api/client";
import type { ActiveCall } from "@/api/types";
import { Badge, stateVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/data-table";
import { Confirm } from "@/components/confirm";
import { ErrorBox, PageHeader } from "@/components/page";
import { useAuth } from "@/hooks/use-auth";
import { dt, duration, money } from "@/lib/utils";

export function LiveCallsPage() {
  const { can } = useAuth();
  const q = useQuery({
    queryKey: ["active-calls"],
    queryFn: () => get<{ items: ActiveCall[]; freeswitch_connected: boolean }>("/calls/active"),
    refetchInterval: 3000,
  });
  const columns = useMemo<ColumnDef<ActiveCall, unknown>[]>(
    () => [
      {
        header: "Started",
        accessorKey: "started_at",
        cell: ({ row }) => <span className="text-xs">{dt(row.original.started_at)}</span>,
      },
      {
        header: "Elapsed",
        cell: ({ row }) => <span className="tabular">{duration(row.original.elapsed_seconds)}</span>,
      },
      { header: "Customer", accessorKey: "customer_name" },
      {
        header: "From",
        accessorKey: "caller_number",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.caller_number}</span>,
      },
      {
        header: "To",
        accessorKey: "called_number",
        cell: ({ row }) => <span className="font-mono text-xs">{row.original.called_number}</span>,
      },
      {
        header: "Carrier",
        cell: ({ row }) =>
          row.original.carrier_name ?? (
            <span className="text-muted-foreground">dialling ({row.original.attempts})</span>
          ),
      },
      {
        header: "State",
        cell: ({ row }) => (
          <span className="flex gap-1">
            <Badge variant={stateVariant(row.original.state)}>{row.original.state}</Badge>
            {!row.original.freeswitch_seen && <Badge variant="warning">not in FreeSWITCH</Badge>}
          </span>
        ),
      },
      {
        header: "Reserved",
        cell: ({ row }) => <span className="tabular">{money(row.original.reserved_amount)}</span>,
      },
      { header: "Max", cell: ({ row }) => duration(row.original.max_call_seconds) },
      {
        id: "actions",
        header: "",
        cell: ({ row }) =>
          can("write") && (
            <Confirm
              trigger={
                <Button variant="ghost" size="sm" aria-label="Hang up">
                  <PhoneOff className="text-danger" />
                </Button>
              }
              title="Hang up this call?"
              description="Both legs are torn down and the call is billed for the seconds used."
              actionLabel="Hang up"
              onConfirm={async () => {
                try {
                  await del(`/calls/active/${row.original.call_uuid}`);
                  toast.success("Hangup sent");
                  void q.refetch();
                } catch (e) {
                  toast.error(e instanceof Error ? e.message : "Hangup failed");
                }
              }}
            />
          ),
      },
    ],
    [can, q],
  );
  return (
    <div>
      <PageHeader
        title="Live calls"
        description="Calls with an open reservation, refreshed every 3 seconds"
        actions={
          <Badge variant={q.data?.freeswitch_connected ? "success" : "danger"}>
            FreeSWITCH {q.data?.freeswitch_connected ? "connected" : "disconnected"}
          </Badge>
        }
      />
      {q.error && <ErrorBox error={q.error} />}
      <DataTable
        columns={columns}
        data={q.data?.items ?? []}
        loading={q.isLoading}
        emptyText="No calls in progress."
      />
    </div>
  );
}
