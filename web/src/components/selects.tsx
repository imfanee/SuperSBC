import { useQuery } from "@tanstack/react-query";
import { get } from "@/api/client";
import type { Listing, RateGroup, RouteGroup, CarrierRow, CustomerRow } from "@/api/types";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

const NONE = "__none__";

function RefSelect({
  value,
  onChange,
  options,
  placeholder,
  allowNone = true,
  testId,
}: {
  value: string | null | undefined;
  onChange: (v: string | null) => void;
  options: Array<{ id: string; name: string; hint?: string }>;
  placeholder: string;
  allowNone?: boolean;
  testId?: string;
}) {
  return (
    <Select value={value ?? NONE} onValueChange={(v) => onChange(v === NONE ? null : v)}>
      <SelectTrigger data-testid={testId}>
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {allowNone && <SelectItem value={NONE}>(none)</SelectItem>}
        {options.map((o) => (
          <SelectItem key={o.id} value={o.id}>
            {o.name}
            {o.hint ? ` (${o.hint})` : ""}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

export function RateGroupSelect(props: {
  value: string | null | undefined;
  onChange: (v: string | null) => void;
  testId?: string;
}) {
  const q = useQuery({
    queryKey: ["rate-groups", "all"],
    queryFn: () => get<Listing<RateGroup>>("/rate-groups", { per_page: 1000 }),
  });
  return (
    <RefSelect
      {...props}
      placeholder="Rate group"
      options={(q.data?.items ?? []).map((g) => ({ id: g.id, name: g.name, hint: g.currency }))}
    />
  );
}

export function RouteGroupSelect(props: {
  value: string | null | undefined;
  onChange: (v: string | null) => void;
  testId?: string;
}) {
  const q = useQuery({
    queryKey: ["route-groups", "all"],
    queryFn: () => get<Listing<RouteGroup>>("/route-groups", { per_page: 1000 }),
  });
  return (
    <RefSelect
      {...props}
      placeholder="Route group"
      options={(q.data?.items ?? []).map((g) => ({ id: g.id, name: g.name }))}
    />
  );
}

export function CarrierSelect(props: {
  value: string | null | undefined;
  onChange: (v: string | null) => void;
  allowNone?: boolean;
  testId?: string;
}) {
  const q = useQuery({
    queryKey: ["carriers", "all"],
    queryFn: () => get<Listing<CarrierRow>>("/carriers", { per_page: 1000 }),
  });
  return (
    <RefSelect
      {...props}
      placeholder="Carrier"
      options={(q.data?.items ?? []).map((c) => ({ id: c.id, name: c.name, hint: c.gateway_host }))}
    />
  );
}

export function CustomerSelect(props: {
  value: string | null | undefined;
  onChange: (v: string | null) => void;
  allowNone?: boolean;
  testId?: string;
}) {
  const q = useQuery({
    queryKey: ["customers", "all"],
    queryFn: () => get<Listing<CustomerRow>>("/customers", { per_page: 1000 }),
  });
  return (
    <RefSelect
      {...props}
      placeholder="Customer"
      options={(q.data?.items ?? []).map((c) => ({ id: c.id, name: c.name }))}
    />
  );
}
