import {
  Activity,
  BarChart3,
  Building2,
  Cable,
  FileText,
  LayoutDashboard,
  ListTree,
  Radio,
  Route as RouteIcon,
  Settings,
  Table2,
  Users,
  type LucideIcon,
} from "lucide-react";

export interface NavItem {
  title: string;
  to: string;
  icon: LucideIcon;
  keywords?: string[];
}

export const navGroups: { title: string; items: NavItem[] }[] = [
  {
    title: "Overview",
    items: [
      { title: "Dashboard", to: "/", icon: LayoutDashboard },
      { title: "Live calls", to: "/calls", icon: Radio, keywords: ["active", "channels"] },
      { title: "CDRs", to: "/cdrs", icon: Table2, keywords: ["calls", "records"] },
      { title: "Reports", to: "/reports", icon: BarChart3, keywords: ["asr", "acd", "revenue"] },
    ],
  },
  {
    title: "Configuration",
    items: [
      { title: "Customers", to: "/customers", icon: Users, keywords: ["clients", "ips"] },
      { title: "Carriers", to: "/carriers", icon: Building2, keywords: ["suppliers", "gateways"] },
      { title: "Rate groups", to: "/rate-groups", icon: FileText, keywords: ["rates", "decks", "prices"] },
      { title: "Route groups", to: "/route-groups", icon: RouteIcon, keywords: ["routing", "prefixes"] },
      { title: "Routing simulator", to: "/simulator", icon: ListTree, keywords: ["test", "simulate"] },
    ],
  },
  {
    title: "System",
    items: [
      { title: "Status", to: "/system", icon: Activity, keywords: ["health", "failover", "settings"] },
      { title: "Users and keys", to: "/system/users", icon: Settings, keywords: ["api keys", "roles"] },
      { title: "Audit log", to: "/system/audit", icon: Cable, keywords: ["changes", "history"] },
    ],
  },
];
