import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api, setCsrfToken, setUnauthorizedHandler } from "@/api/client";
import type { Me } from "@/api/types";

interface AuthState {
  user: Me | null;
  loading: boolean;
  login: (email: string, password: string, totp?: string) => Promise<Me>;
  logout: () => Promise<void>;
  reload: () => Promise<void>;
  can: (action: "write" | "money" | "admin") => boolean;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(async () => {
    try {
      const me = await api<Me>("/auth/me", { retry: true });
      setCsrfToken(me.csrf_token);
      setUser(me);
    } catch {
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => setUser(null));
    void reload();
  }, [reload]);

  const login = useCallback(async (email: string, password: string, totp?: string) => {
    const me = await api<Me>("/auth/login", {
      method: "POST",
      body: { email, password, totp_code: totp ?? "" },
      retry: false,
    });
    setCsrfToken(me.csrf_token);
    setUser(me);
    return me;
  }, []);

  const logout = useCallback(async () => {
    try {
      await api("/auth/logout", { method: "POST", retry: false });
    } finally {
      setUser(null);
    }
  }, []);

  const can = useCallback(
    (action: "write" | "money" | "admin") => {
      if (!user) return false;
      if (user.role === "admin") return true;
      if (action === "write") return user.role === "operator";
      return false;
    },
    [user],
  );

  const value = useMemo(
    () => ({ user, loading, login, logout, reload, can }),
    [user, loading, login, logout, reload, can],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}
