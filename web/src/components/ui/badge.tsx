import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium transition-colors whitespace-nowrap",
  {
    variants: {
      variant: {
        default: "border-transparent bg-primary text-primary-foreground",
        secondary: "border-transparent bg-secondary text-secondary-foreground",
        outline: "text-foreground",
        success: "border-transparent bg-success/15 text-success",
        danger: "border-transparent bg-danger/15 text-danger",
        warning: "border-transparent bg-warning/20 text-warning",
        info: "border-transparent bg-info/15 text-info",
      },
    },
    defaultVariants: { variant: "default" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}

/** Maps a call disposition or gateway state to a badge variant. */
export function stateVariant(state: string | null | undefined): BadgeProps["variant"] {
  switch ((state ?? "").toLowerCase()) {
    case "answered":
    case "up":
    case "active":
    case "ok":
    case "reged":
    case "noreg":
      return "success";
    case "failed":
    case "rejected_auth":
    case "rejected_balance":
    case "rejected_route":
    case "down":
    case "blocked":
    case "disabled":
      return "danger";
    case "busy":
    case "no_answer":
    case "cancelled":
    case "degraded":
    case "suspended":
      return "warning";
    case "pending":
    case "setup":
    case "ringing":
    case "early":
    case "unknown":
    case "noping":
      return "info";
    default:
      return "secondary";
  }
}

export { Badge, badgeVariants };
