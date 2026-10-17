import type { ReactNode } from "react";
import type { LucideIcon } from "lucide-react";

/** One band of the page, with the same gutters everywhere. */
export function Section({
  children,
  className = "",
  muted = false,
}: {
  children: ReactNode;
  className?: string;
  muted?: boolean;
}) {
  return (
    <section className={`${muted ? "bg-muted/30 border-y" : ""} ${className}`}>
      <div className="mx-auto w-full max-w-6xl px-4 py-16 sm:px-6 sm:py-20">{children}</div>
    </section>
  );
}

export function Eyebrow({ children }: { children: ReactNode }) {
  return <p className="text-xs font-semibold uppercase tracking-[0.12em] text-primary">{children}</p>;
}

export function SectionHeading({
  eyebrow,
  title,
  lead,
  center = false,
}: {
  eyebrow?: string;
  title: string;
  lead?: string;
  center?: boolean;
}) {
  return (
    <div className={`max-w-2xl ${center ? "mx-auto text-center" : ""}`}>
      {eyebrow && <Eyebrow>{eyebrow}</Eyebrow>}
      <h2 className="mt-2 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">{title}</h2>
      {lead && <p className="mt-4 text-pretty text-base leading-relaxed text-muted-foreground">{lead}</p>}
    </div>
  );
}

/** A capability: what it is, and the detail that proves it is real. */
export function FeatureCard({
  icon: Icon,
  title,
  children,
  points,
}: {
  icon: LucideIcon;
  title: string;
  children: ReactNode;
  points?: string[];
}) {
  return (
    <div className="rounded-xl border bg-card p-6 transition-shadow hover:shadow-sm">
      <span className="grid h-10 w-10 place-items-center rounded-lg bg-primary/10 text-primary">
        <Icon className="h-5 w-5" />
      </span>
      <h3 className="mt-4 font-semibold">{title}</h3>
      <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{children}</p>
      {points && (
        <ul className="mt-4 space-y-1.5 text-sm text-muted-foreground">
          {points.map((p) => (
            <li key={p} className="flex gap-2">
              <span aria-hidden className="mt-2 h-1 w-1 shrink-0 rounded-full bg-primary" />
              <span>{p}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** A number worth stating plainly, with what it means underneath. */
export function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className="rounded-xl border bg-card px-5 py-6 text-center">
      <div className="text-2xl font-semibold tracking-tight sm:text-3xl">{value}</div>
      <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{label}</div>
    </div>
  );
}
