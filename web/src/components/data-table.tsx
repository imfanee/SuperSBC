import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import type { ReactNode } from "react";

export interface DataTableProps<T> {
  columns: ColumnDef<T, unknown>[];
  data: T[];
  total?: number;
  page?: number;
  perPage?: number;
  onPageChange?: (page: number) => void;
  sorting?: SortingState;
  onSortingChange?: (s: SortingState) => void;
  loading?: boolean;
  onRowClick?: (row: T) => void;
  emptyText?: string;
  renderExpanded?: (row: T) => ReactNode;
  expandedId?: string | null;
  rowId?: (row: T) => string;
  /** Under 640 px rows render as cards using the column headers as labels. */
  cardBreakpoint?: boolean;
}

export function DataTable<T>({
  columns,
  data,
  total,
  page = 1,
  perPage = 50,
  onPageChange,
  sorting,
  onSortingChange,
  loading,
  onRowClick,
  emptyText = "Nothing to show.",
  renderExpanded,
  expandedId,
  rowId,
  cardBreakpoint = true,
}: DataTableProps<T>) {
  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualSorting: true,
    state: { sorting: sorting ?? [] },
    onSortingChange: (u) => onSortingChange?.(typeof u === "function" ? u(sorting ?? []) : u),
  });
  const pages = total !== undefined ? Math.max(1, Math.ceil(total / perPage)) : undefined;
  return (
    <div className="space-y-2">
      <div
        className={cn("rounded-md border bg-card", cardBreakpoint && "max-sm:border-0 max-sm:bg-transparent")}
      >
        <Table className={cn(cardBreakpoint && "max-sm:block")}>
          <TableHeader className={cn(cardBreakpoint && "max-sm:hidden")}>
            {table.getHeaderGroups().map((hg) => (
              <TableRow key={hg.id}>
                {hg.headers.map((header) => {
                  const canSort = header.column.getCanSort() && !!onSortingChange;
                  const dir = header.column.getIsSorted();
                  return (
                    <TableHead
                      key={header.id}
                      style={{ width: header.getSize() !== 150 ? header.getSize() : undefined }}
                    >
                      {header.isPlaceholder ? null : canSort ? (
                        <button
                          className="inline-flex items-center gap-1 hover:text-foreground"
                          onClick={header.column.getToggleSortingHandler()}
                        >
                          {flexRender(header.column.columnDef.header, header.getContext())}
                          {dir === "asc" ? (
                            <ArrowUp className="h-3 w-3" />
                          ) : dir === "desc" ? (
                            <ArrowDown className="h-3 w-3" />
                          ) : (
                            <ArrowUpDown className="h-3 w-3 opacity-40" />
                          )}
                        </button>
                      ) : (
                        flexRender(header.column.columnDef.header, header.getContext())
                      )}
                    </TableHead>
                  );
                })}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody className={cn(cardBreakpoint && "max-sm:block max-sm:space-y-2")}>
            {loading && data.length === 0 ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  {columns.map((_, j) => (
                    <TableCell key={j}>
                      <Skeleton className="h-4 w-full" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : data.length === 0 ? (
              <TableRow>
                <TableCell colSpan={columns.length} className="h-20 text-center text-muted-foreground">
                  {emptyText}
                </TableCell>
              </TableRow>
            ) : (
              table.getRowModel().rows.map((row) => {
                const id = rowId ? rowId(row.original) : row.id;
                const expanded = renderExpanded && expandedId === id;
                return (
                  <>
                    <TableRow
                      key={row.id}
                      onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                      className={cn(
                        onRowClick && "cursor-pointer",
                        cardBreakpoint &&
                          "max-sm:block max-sm:rounded-md max-sm:border max-sm:bg-card max-sm:p-2",
                      )}
                    >
                      {row.getVisibleCells().map((cell) => (
                        <TableCell
                          key={cell.id}
                          className={cn(
                            cardBreakpoint &&
                              "max-sm:flex max-sm:justify-between max-sm:gap-3 max-sm:py-1 max-sm:before:text-xs max-sm:before:text-muted-foreground max-sm:before:content-[attr(data-label)]",
                          )}
                          data-label={
                            typeof cell.column.columnDef.header === "string"
                              ? cell.column.columnDef.header
                              : ""
                          }
                        >
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </TableCell>
                      ))}
                    </TableRow>
                    {expanded && (
                      <TableRow key={row.id + "-x"} className="bg-muted/30 hover:bg-muted/30">
                        <TableCell colSpan={columns.length} className="p-3">
                          {renderExpanded(row.original)}
                        </TableCell>
                      </TableRow>
                    )}
                  </>
                );
              })
            )}
          </TableBody>
        </Table>
      </div>
      {pages !== undefined && onPageChange && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <div>
            {total} rows, page {page} of {pages}
          </div>
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="sm"
              disabled={page <= 1}
              onClick={() => onPageChange(page - 1)}
              aria-label="Previous page"
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={page >= pages}
              onClick={() => onPageChange(page + 1)}
              aria-label="Next page"
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
