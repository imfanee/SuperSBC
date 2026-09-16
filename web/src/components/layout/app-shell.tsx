import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { ChevronsLeft, LogOut, Menu, PhoneForwarded, Search, User } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Badge } from "@/components/ui/badge";
import { ThemeToggle } from "@/components/layout/theme";
import { navGroups } from "@/components/layout/nav";
import { useAuth } from "@/hooks/use-auth";
import { cn } from "@/lib/utils";

function SidebarNav({ collapsed, onNavigate }: { collapsed: boolean; onNavigate?: () => void }) {
  return (
    <nav className="flex flex-1 flex-col gap-4 overflow-y-auto px-2 py-3">
      {navGroups.map((g) => (
        <div key={g.title}>
          {!collapsed && (
            <div className="mb-1 px-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
              {g.title}
            </div>
          )}
          <ul className="space-y-0.5">
            {g.items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  end={item.to === "/" || item.to === "/system"}
                  onClick={onNavigate}
                  title={item.title}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-sidebar-foreground/80 hover:bg-accent hover:text-accent-foreground",
                      isActive && "bg-primary/10 text-primary font-medium",
                      collapsed && "justify-center",
                    )
                  }
                >
                  <item.icon className="h-4 w-4 shrink-0" />
                  {!collapsed && <span className="truncate">{item.title}</span>}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

export function AppShell() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem("sbc.sidebar") === "collapsed";
    } catch {
      return false;
    }
  });
  const [mobileOpen, setMobileOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);

  useEffect(() => {
    try {
      localStorage.setItem("sbc.sidebar", collapsed ? "collapsed" : "open");
    } catch {
      /* ignore */
    }
  }, [collapsed]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => setMobileOpen(false), [location.pathname]);

  const allItems = navGroups.flatMap((g) => g.items);

  return (
    <div className="flex min-h-screen w-full">
      <aside
        className={cn(
          "hidden lg:flex flex-col border-r bg-sidebar transition-[width] duration-200",
          collapsed ? "w-14" : "w-60",
        )}
      >
        <div className={cn("flex h-14 items-center gap-2 border-b px-3", collapsed && "justify-center px-0")}>
          <PhoneForwarded className="h-5 w-5 text-primary" />
          {!collapsed && <span className="font-semibold tracking-tight">SuperSBC</span>}
        </div>
        <SidebarNav collapsed={collapsed} />
        <div className="border-t p-2">
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-center"
            onClick={() => setCollapsed((c) => !c)}
            aria-label="Collapse sidebar"
          >
            <ChevronsLeft className={cn("h-4 w-4 transition-transform", collapsed && "rotate-180")} />
          </Button>
        </div>
      </aside>

      <Dialog open={mobileOpen} onOpenChange={setMobileOpen}>
        <DialogContent side="right" className="w-72 p-0 sm:max-w-xs left-0 right-auto">
          <DialogTitle className="flex h-14 items-center gap-2 border-b px-4">
            <PhoneForwarded className="h-5 w-5 text-primary" /> SuperSBC
          </DialogTitle>
          <SidebarNav collapsed={false} onNavigate={() => setMobileOpen(false)} />
        </DialogContent>
      </Dialog>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b bg-background/95 px-4 backdrop-blur">
          <Button
            variant="ghost"
            size="icon"
            className="lg:hidden"
            onClick={() => setMobileOpen(true)}
            aria-label="Open menu"
          >
            <Menu className="h-5 w-5" />
          </Button>
          <Button
            variant="outline"
            className="hidden h-8 w-64 justify-start gap-2 text-muted-foreground sm:flex"
            onClick={() => setPaletteOpen(true)}
          >
            <Search className="h-4 w-4" />
            <span className="text-xs">Search or jump to</span>
            <kbd className="ml-auto rounded border bg-muted px-1.5 text-[10px]">Ctrl K</kbd>
          </Button>
          <div className="ml-auto flex items-center gap-1">
            <ThemeToggle />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="sm" className="gap-2">
                  <User className="h-4 w-4" />
                  <span className="hidden max-w-[160px] truncate sm:inline">{user?.email}</span>
                  <Badge variant="secondary" className="hidden sm:inline-flex">
                    {user?.role}
                  </Badge>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="font-normal">
                  <div className="truncate text-sm">{user?.email}</div>
                  <div className="text-xs text-muted-foreground">role: {user?.role}</div>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => navigate("/account")}>
                  Account and security
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => void logout().then(() => navigate("/login"))}>
                  <LogOut /> Log out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        <main className="flex-1 p-4 md:p-6">
          <Outlet />
        </main>
      </div>

      <CommandDialog open={paletteOpen} onOpenChange={setPaletteOpen}>
        <CommandInput placeholder="Type a page name..." />
        <CommandList>
          <CommandEmpty>No results.</CommandEmpty>
          <CommandGroup heading="Pages">
            {allItems.map((item) => (
              <CommandItem
                key={item.to}
                value={`${item.title} ${(item.keywords ?? []).join(" ")}`}
                onSelect={() => {
                  setPaletteOpen(false);
                  navigate(item.to);
                }}
              >
                <item.icon /> {item.title}
              </CommandItem>
            ))}
          </CommandGroup>
        </CommandList>
      </CommandDialog>
    </div>
  );
}
