import { useState, type ReactNode } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Input } from "@/components/ui/input";

/**
 * Destructive confirmation. When `typedName` is given the user must type it
 * (customers and carriers) before the action is enabled.
 */
export function Confirm({
  trigger,
  title,
  description,
  typedName,
  actionLabel = "Delete",
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: ReactNode;
  typedName?: string;
  actionLabel?: string;
  onConfirm: () => void | Promise<void>;
}) {
  const [typed, setTyped] = useState("");
  const ready = !typedName || typed === typedName;
  return (
    <AlertDialog onOpenChange={() => setTyped("")}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2">
              {description && <div>{description}</div>}
              {typedName && (
                <div>
                  <div className="mb-1">
                    Type <span className="font-mono font-semibold text-foreground">{typedName}</span> to
                    confirm.
                  </div>
                  <Input
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                    placeholder={typedName}
                    autoFocus
                  />
                </div>
              )}
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!ready}
            className="bg-destructive hover:bg-destructive/90"
            onClick={() => void onConfirm()}
          >
            {actionLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
