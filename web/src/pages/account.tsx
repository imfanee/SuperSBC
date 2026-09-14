import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { del, get, post } from "@/api/client";
import type { APIKey } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/form";
import { PageHeader } from "@/components/page";
import { useAuth } from "@/hooks/use-auth";
import { dt } from "@/lib/utils";

export function AccountPage() {
  const { user, reload, logout } = useAuth();
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["my-keys"], queryFn: () => get<APIKey[]>("/auth/api-keys") });
  const [pw, setPw] = useState({ current: "", next: "" });
  const [totp, setTotp] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [code, setCode] = useState("");
  const [keyName, setKeyName] = useState("");
  const [newKey, setNewKey] = useState<string | null>(null);
  return (
    <div>
      <PageHeader title="Account and security" description={user?.email} />
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>Change password</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            <Field label="Current password">
              <Input
                type="password"
                value={pw.current}
                onChange={(e) => setPw({ ...pw, current: e.target.value })}
                autoComplete="current-password"
              />
            </Field>
            <Field label="New password" hint="at least 10 characters">
              <Input
                type="password"
                value={pw.next}
                onChange={(e) => setPw({ ...pw, next: e.target.value })}
                autoComplete="new-password"
              />
            </Field>
            <Button
              disabled={!pw.current || pw.next.length < 10}
              onClick={() =>
                post("/auth/password", { current_password: pw.current, new_password: pw.next })
                  .then(() => (toast.success("Password changed, sign in again"), logout()))
                  .catch((e) => toast.error(e.message))
              }
            >
              Change
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>
              Two-factor authentication{" "}
              {user?.totp_enabled ? (
                <Badge variant="success">enabled</Badge>
              ) : (
                <Badge variant="secondary">off</Badge>
              )}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {!user?.totp_enabled && !totp && (
              <Button
                variant="outline"
                onClick={() =>
                  post<{ secret: string; otpauth_url: string }>("/auth/totp/setup")
                    .then(setTotp)
                    .catch((e) => toast.error(e.message))
                }
              >
                Set up TOTP
              </Button>
            )}
            {totp && (
              <div className="space-y-2">
                <div>
                  Add this secret to your authenticator app: <code className="break-all">{totp.secret}</code>
                </div>
                <div className="text-xs text-muted-foreground break-all">{totp.otpauth_url}</div>
                <Input
                  placeholder="6 digit code"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  className="w-40"
                />
                <Button
                  onClick={() =>
                    post("/auth/totp/confirm", { code })
                      .then(() => (toast.success("2FA enabled"), setTotp(null), reload()))
                      .catch((e) => toast.error(e.message))
                  }
                >
                  Confirm
                </Button>
              </div>
            )}
            {user?.totp_enabled && (
              <div className="space-y-2">
                <Input
                  placeholder="current code to disable"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  className="w-40"
                />
                <Button
                  variant="destructive"
                  onClick={() =>
                    post("/auth/totp/disable", { code })
                      .then(() => (toast.success("2FA disabled"), reload()))
                      .catch((e) => toast.error(e.message))
                  }
                >
                  Disable 2FA
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>API keys</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex gap-2">
              <Input placeholder="key name" value={keyName} onChange={(e) => setKeyName(e.target.value)} />
              <Button
                disabled={!keyName}
                onClick={() =>
                  post<{ key: string }>("/auth/api-keys", { name: keyName })
                    .then(
                      (r) => (
                        setNewKey(r.key),
                        setKeyName(""),
                        qc.invalidateQueries({ queryKey: ["my-keys"] })
                      ),
                    )
                    .catch((e) => toast.error(e.message))
                }
              >
                Create
              </Button>
            </div>
            {newKey && (
              <div className="rounded-md border border-info/40 bg-info/10 p-2 text-xs">
                Copy it now, it is shown once: <code className="break-all">{newKey}</code>
                <div className="mt-1 text-muted-foreground">
                  Use as Authorization: Bearer {newKey.slice(0, 12)}...
                </div>
              </div>
            )}
            <ul className="divide-y">
              {(keys.data ?? []).map((k) => (
                <li key={k.id} className="flex items-center justify-between py-1">
                  <span>
                    {k.name}{" "}
                    <span className="font-mono text-xs text-muted-foreground">{k.key_prefix}...</span>{" "}
                    <span className="text-xs text-muted-foreground">
                      last used {dt(k.last_used_at) || "never"}
                    </span>
                  </span>
                  {k.revoked_at ? (
                    <Badge variant="danger">revoked</Badge>
                  ) : (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() =>
                        del(`/auth/api-keys/${k.id}`).then(() =>
                          qc.invalidateQueries({ queryKey: ["my-keys"] }),
                        )
                      }
                    >
                      Revoke
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
