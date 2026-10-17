import { useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router-dom";
import { ArrowRight, Menu, PhoneForwarded, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { site } from "@/site.config";

const pages = [
  { to: "/", label: "Overview" },
  { to: "/features", label: "Capabilities" },
  { to: "/security", label: "Security" },
  { to: "/contact", label: "Contact" },
];

/** The mark from the portal sidebar, so both halves look like one product. */
export function Wordmark({ className = "" }: { className?: string }) {
  return (
    <span className={`flex items-center gap-2 font-semibold tracking-tight ${className}`}>
      <span className="grid h-8 w-8 place-items-center rounded-lg bg-primary text-primary-foreground">
        <PhoneForwarded className="h-4 w-4" />
      </span>
      {site.name}
    </span>
  );
}

/** Public site shell: the same header and footer on every page, and a login
 * button that is always one click away (D-82). */
export function MarketingLayout() {
  const [open, setOpen] = useState(false);
  const location = useLocation();
  return (
    <div className="flex min-h-dvh flex-col bg-background">
      <header className="sticky top-0 z-40 border-b bg-background/85 backdrop-blur">
        <div className="mx-auto flex h-16 w-full max-w-6xl items-center justify-between gap-4 px-4 sm:px-6">
          <Link to="/" className="shrink-0" onClick={() => setOpen(false)}>
            <Wordmark />
          </Link>
          <nav className="hidden items-center gap-1 md:flex">
            {pages.map((p) => (
              <NavLink
                key={p.to}
                to={p.to}
                end={p.to === "/"}
                className={({ isActive }) =>
                  `rounded-md px-3 py-2 text-sm transition-colors ${
                    isActive
                      ? "bg-muted font-medium text-foreground"
                      : "text-muted-foreground hover:bg-muted/60 hover:text-foreground"
                  }`
                }
              >
                {p.label}
              </NavLink>
            ))}
          </nav>
          <div className="flex items-center gap-2">
            <Button asChild size="sm" className="hidden sm:inline-flex">
              <Link to={site.portalPath} data-testid="header-login">
                Log in <ArrowRight />
              </Link>
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="md:hidden"
              aria-label={open ? "Close menu" : "Open menu"}
              aria-expanded={open}
              onClick={() => setOpen((v) => !v)}
            >
              {open ? <X /> : <Menu />}
            </Button>
          </div>
        </div>
        {open && (
          <nav className="border-t bg-background px-4 pb-4 pt-2 md:hidden">
            {pages.map((p) => (
              <Link
                key={p.to}
                to={p.to}
                onClick={() => setOpen(false)}
                className={`block rounded-md px-3 py-2.5 text-sm ${
                  location.pathname === p.to ? "bg-muted font-medium" : "text-muted-foreground"
                }`}
              >
                {p.label}
              </Link>
            ))}
            <Button asChild size="sm" className="mt-2 w-full">
              <Link to={site.portalPath} onClick={() => setOpen(false)}>
                Log in <ArrowRight />
              </Link>
            </Button>
          </nav>
        )}
      </header>

      <main className="flex-1">
        <Outlet />
      </main>

      <footer className="border-t bg-muted/30">
        <div className="mx-auto grid w-full max-w-6xl gap-8 px-4 py-12 sm:px-6 md:grid-cols-[1.4fr_1fr_1fr]">
          <div>
            <Wordmark />
            <p className="mt-3 max-w-sm text-sm text-muted-foreground">{site.summary}</p>
          </div>
          <div>
            <h3 className="text-sm font-semibold">Product</h3>
            <ul className="mt-3 space-y-2 text-sm text-muted-foreground">
              {pages.slice(1).map((p) => (
                <li key={p.to}>
                  <Link to={p.to} className="hover:text-foreground">
                    {p.label}
                  </Link>
                </li>
              ))}
              <li>
                <Link to={site.portalPath} className="hover:text-foreground">
                  Operator portal
                </Link>
              </li>
            </ul>
          </div>
          <div>
            <h3 className="text-sm font-semibold">Contact</h3>
            <ul className="mt-3 space-y-2 text-sm text-muted-foreground">
              <li>{site.contact.company}</li>
              <li>
                <a href={`mailto:${site.contact.email}`} className="hover:text-foreground">
                  {site.contact.email}
                </a>
              </li>
              <li>
                <a
                  href={`tel:${site.contact.phone.replace(/[^+\d]/g, "")}`}
                  className="hover:text-foreground"
                >
                  {site.contact.phone}
                </a>
              </li>
            </ul>
          </div>
        </div>
        <div className="border-t">
          <div className="mx-auto flex w-full max-w-6xl flex-col gap-2 px-4 py-5 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between sm:px-6">
            <span>
              {site.name} by {site.contact.company}
            </span>
            <span>Self hosted. Your SIP, your database, your margins.</span>
          </div>
        </div>
      </footer>
    </div>
  );
}
