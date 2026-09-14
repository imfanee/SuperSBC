import type { ReactNode } from "react";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/** Labelled form field with an error line under it. */
export function Field({
  label,
  error,
  hint,
  children,
  className,
}: {
  label: string;
  error?: string;
  hint?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("space-y-1", className)}>
      <Label>{label}</Label>
      {children}
      {hint && !error && <div className="text-xs text-muted-foreground">{hint}</div>}
      {error && <div className="text-xs text-danger">{error}</div>}
    </div>
  );
}
