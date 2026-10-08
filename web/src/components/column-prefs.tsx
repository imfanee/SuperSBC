import { useMemo, useState } from "react";
import type { ColumnDef, VisibilityState } from "@tanstack/react-table";
import { ArrowDown, ArrowUp, Columns3 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/** Stable id of a column definition (explicit id, else its accessor key). */
function colID<T>(c: ColumnDef<T, unknown>): string {
  return String(c.id ?? (c as { accessorKey?: string }).accessorKey);
}

function readStored<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback; // private browsing: preferences simply do not persist
  }
}

function writeStored(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* ignore */
  }
}

export interface ColumnPrefs<T> {
  hidden: VisibilityState;
  order: string[];
  /** Columns in display order, for the menu. */
  ordered: ColumnDef<T, unknown>[];
  setColumn: (id: string, visible: boolean) => void;
  moveColumn: (id: string, by: number) => void;
  reset: () => void;
}

/**
 * Per browser column layout of a table (D-74): which columns are visible and
 * in what order, remembered in localStorage under `key`. A stored order that
 * does not mention a column (one added by a later release) keeps that column
 * after the ones it lists, so an upgrade never hides new data.
 */
export function useColumnPrefs<T>(key: string, columns: ColumnDef<T, unknown>[]): ColumnPrefs<T> {
  const visKey = `${key}.columns`;
  const orderKey = `${key}.column-order`;
  const [hidden, setHidden] = useState<VisibilityState>(() => readStored<VisibilityState>(visKey, {}));
  const [order, setOrder] = useState<string[]>(() => readStored<string[]>(orderKey, []));
  const ordered = useMemo(() => {
    const byId = new Map(columns.map((c) => [colID(c), c]));
    const out = order.map((id) => byId.get(id)).filter(Boolean) as ColumnDef<T, unknown>[];
    for (const c of columns) if (!order.includes(colID(c))) out.push(c);
    return out;
  }, [columns, order]);
  return {
    hidden,
    order,
    ordered,
    setColumn: (id, visible) => {
      const next = { ...hidden, [id]: visible };
      setHidden(next);
      writeStored(visKey, next);
    },
    moveColumn: (id, by) => {
      const ids = ordered.map(colID);
      const i = ids.indexOf(id);
      const j = i + by;
      if (i < 0 || j < 0 || j >= ids.length) return;
      [ids[i], ids[j]] = [ids[j], ids[i]];
      setOrder(ids);
      writeStored(orderKey, ids);
    },
    reset: () => {
      setHidden({});
      setOrder([]);
      try {
        localStorage.removeItem(visKey);
        localStorage.removeItem(orderKey);
      } catch {
        /* ignore */
      }
    },
  };
}

/** The Columns button: tick to show or hide, arrows to reorder. */
export function ColumnsMenu<T>({ prefs, testId = "columns" }: { prefs: ColumnPrefs<T>; testId?: string }) {
  const { hidden, ordered, setColumn, moveColumn, reset } = prefs;
  const visibleCount = ordered.filter((c) => hidden[colID(c)] !== false).length;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" data-testid={`${testId}-menu`}>
          <Columns3 /> Columns
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-96 overflow-y-auto">
        <DropdownMenuLabel className="text-muted-foreground">
          Tick to show, arrows to reorder
        </DropdownMenuLabel>
        {ordered.map((c, i) => {
          const id = colID(c);
          const label = typeof c.header === "string" && c.header ? c.header : id;
          const visible = hidden[id] !== false;
          return (
            <DropdownMenuCheckboxItem
              key={id}
              checked={visible}
              // keep at least one column so the table never collapses
              disabled={visible && visibleCount === 1}
              onCheckedChange={(v) => setColumn(id, v === true)}
              onSelect={(e) => e.preventDefault()}
              data-testid={`${testId}-${id}`}
            >
              <span className="flex-1 pr-4">{label}</span>
              <span className="ml-auto flex items-center gap-0.5" onClick={(e) => e.stopPropagation()}>
                <button
                  type="button"
                  className="rounded p-0.5 hover:bg-background disabled:opacity-30"
                  aria-label={`Move ${label} up`}
                  disabled={i === 0}
                  onClick={() => moveColumn(id, -1)}
                  data-testid={`${testId}-up-${id}`}
                >
                  <ArrowUp className="size-3.5" />
                </button>
                <button
                  type="button"
                  className="rounded p-0.5 hover:bg-background disabled:opacity-30"
                  aria-label={`Move ${label} down`}
                  disabled={i === ordered.length - 1}
                  onClick={() => moveColumn(id, 1)}
                  data-testid={`${testId}-down-${id}`}
                >
                  <ArrowDown className="size-3.5" />
                </button>
              </span>
            </DropdownMenuCheckboxItem>
          );
        })}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={reset} data-testid={`${testId}-reset`}>
          Show all columns
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
