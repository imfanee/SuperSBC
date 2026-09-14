import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AppShell } from "@/components/layout/app-shell";
import { useAuth } from "@/hooks/use-auth";
import { LoginPage, ForgotPasswordPage, ResetPasswordPage } from "@/pages/login";
import { DashboardPage } from "@/pages/dashboard";
import { CustomersPage, CustomerDetailPage } from "@/pages/customers";
import { CarriersPage, CarrierDetailPage } from "@/pages/carriers";
import { RateGroupsPage, RateGroupDetailPage } from "@/pages/rate-groups";
import { RouteGroupsPage, RouteGroupDetailPage } from "@/pages/route-groups";
import { SimulatorPage } from "@/pages/simulator";
import { CDRsPage } from "@/pages/cdrs";
import { CallTracePage } from "@/pages/call-trace";
import { ReportsPage } from "@/pages/reports";
import { LiveCallsPage } from "@/pages/live-calls";
import { SystemPage, UsersPage, AuditPage } from "@/pages/system";
import { AccountPage } from "@/pages/account";
import { Skeleton } from "@/components/ui/skeleton";

function RequireAuth({ children }: { children: React.ReactElement }) {
  const { user, loading } = useAuth();
  const location = useLocation();
  if (loading) {
    return (
      <div className="p-8">
        <Skeleton className="h-8 w-48" />
      </div>
    );
  }
  if (!user) return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  return children;
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      <Route path="/reset-password" element={<ResetPasswordPage />} />
      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route path="/" element={<DashboardPage />} />
        <Route path="/calls" element={<LiveCallsPage />} />
        <Route path="/cdrs" element={<CDRsPage />} />
        <Route path="/cdrs/:id/trace" element={<CallTracePage />} />
        <Route path="/reports" element={<ReportsPage />} />
        <Route path="/customers" element={<CustomersPage />} />
        <Route path="/customers/:id" element={<CustomerDetailPage />} />
        <Route path="/carriers" element={<CarriersPage />} />
        <Route path="/carriers/:id" element={<CarrierDetailPage />} />
        <Route path="/rate-groups" element={<RateGroupsPage />} />
        <Route path="/rate-groups/:id" element={<RateGroupDetailPage />} />
        <Route path="/route-groups" element={<RouteGroupsPage />} />
        <Route path="/route-groups/:id" element={<RouteGroupDetailPage />} />
        <Route path="/simulator" element={<SimulatorPage />} />
        <Route path="/system" element={<SystemPage />} />
        <Route path="/system/users" element={<UsersPage />} />
        <Route path="/system/audit" element={<AuditPage />} />
        <Route path="/account" element={<AccountPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
