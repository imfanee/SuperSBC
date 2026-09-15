import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { del, get, post } from "@/api/client";
import type { HeaderRule } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Field } from "@/components/form";
import { useAuth } from "@/hooks/use-auth";

/** Header manipulation rules of a customer or carrier (Section 7). */
export function HeaderRulesTab({
  ownerType,
  ownerId,
}: {
  ownerType: "customer" | "carrier";
  ownerId: string;
}) {
  const { can } = useAuth();
  const qc = useQueryClient();
  const base = `/${ownerType}s/${ownerId}/header-rules`;
  const q = useQuery({
    queryKey: ["header-rules", ownerType, ownerId],
    queryFn: () => get<HeaderRule[]>(base),
  });
  const [form, setForm] = useState({
    direction: "egress",
    action: "add",
    header: "",
    value: "",
    priority: "100",
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ["header-rules", ownerType, ownerId] });
  return (
    <Card>
      <CardHeader>
        <CardTitle>Header rules</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Egress rules shape the INVITE sent to the carrier; response rules add headers to responses sent to
          the customer.
          {ownerType === "customer"
            ? " Customer rules apply to every call it originates; a carrier's rule for the same header wins."
            : " Carrier rules apply to every call it receives and override the customer's rule for the same header."}{" "}
          <em>add</em> sets a value (placeholders:{" "}
          {"{caller} {called} {customer} {carrier} {call_uuid} {node_ip}"}), <em>passthrough</em> copies the
          customer's header, <em>remove</em> cancels a lower priority add or passthrough. Standard identity
          headers are never copied by default.
        </p>
        {can("write") && (
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              post(base, { ...form, priority: Number(form.priority) || 100 })
                .then(
                  () => (toast.success("Rule added"), setForm({ ...form, header: "", value: "" }), refresh()),
                )
                .catch((err) => toast.error(err.message));
            }}
          >
            <Field label="Direction">
              <Select value={form.direction} onValueChange={(v) => setForm({ ...form, direction: v })}>
                <SelectTrigger className="w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="egress">egress</SelectItem>
                  <SelectItem value="response">response</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="Action">
              <Select value={form.action} onValueChange={(v) => setForm({ ...form, action: v })}>
                <SelectTrigger className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="add">add</SelectItem>
                  <SelectItem value="passthrough">passthrough</SelectItem>
                  <SelectItem value="remove">remove</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="Header">
              <Input
                value={form.header}
                onChange={(e) => setForm({ ...form, header: e.target.value })}
                placeholder="X-Account-Id"
                className="w-44"
                data-testid="rule-header"
              />
            </Field>
            <Field label="Value">
              <Input
                value={form.value}
                onChange={(e) => setForm({ ...form, value: e.target.value })}
                placeholder="acct-{customer}"
                className="w-56"
                disabled={form.action !== "add"}
              />
            </Field>
            <Field label="Priority">
              <Input
                value={form.priority}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
                className="w-20"
              />
            </Field>
            <Button type="submit" disabled={!form.header} data-testid="rule-add">
              <Plus /> Add rule
            </Button>
          </form>
        )}
        <table className="w-full text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr>
              <th className="py-1 text-left">Priority</th>
              <th className="py-1 text-left">Direction</th>
              <th className="py-1 text-left">Action</th>
              <th className="py-1 text-left">Header</th>
              <th className="py-1 text-left">Value</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {(q.data ?? []).map((r) => (
              <tr key={r.id} className="border-t">
                <td className="py-1 tabular">{r.priority}</td>
                <td className="py-1">
                  <Badge variant="secondary">{r.direction}</Badge>
                </td>
                <td className="py-1">
                  <Badge
                    variant={
                      r.action === "remove" ? "danger" : r.action === "passthrough" ? "info" : "success"
                    }
                  >
                    {r.action}
                  </Badge>
                </td>
                <td className="py-1 font-mono">{r.header}</td>
                <td className="py-1 font-mono text-xs">{r.value}</td>
                <td className="py-1 text-right">
                  {can("write") && (
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label="Delete rule"
                      onClick={() => del(`/header-rules/${r.id}`).then(refresh)}
                    >
                      <Trash2 className="text-danger" />
                    </Button>
                  )}
                </td>
              </tr>
            ))}
            {q.data?.length === 0 && (
              <tr>
                <td colSpan={6} className="py-3 text-center text-muted-foreground">
                  No rules: only the SBC's own headers are sent.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </CardContent>
    </Card>
  );
}
