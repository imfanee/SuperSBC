import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { PhoneForwarded } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field } from "@/components/form";
import { CreditFooter } from "@/components/layout/credit-footer";
import { useAuth } from "@/hooks/use-auth";
import { ApiError, get, post } from "@/api/client";

const schema = z.object({
  email: z.string().email("Enter a valid email"),
  password: z.string().min(1, "Password required"),
  totp: z.string().optional(),
});
type Form = z.infer<typeof schema>;

function AuthFrame({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-screen flex-col bg-muted/40">
      <div className="flex flex-1 items-center justify-center p-4">
        <Card className="w-full max-w-sm">
          <CardHeader className="items-center text-center">
            <PhoneForwarded className="mb-2 h-8 w-8 text-primary" />
            <CardTitle className="text-lg">{title}</CardTitle>
            <CardDescription>{description}</CardDescription>
          </CardHeader>
          <CardContent>{children}</CardContent>
        </Card>
      </div>
      <CreditFooter />
    </div>
  );
}

export function LoginPage() {
  const { login, user } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [needTotp, setNeedTotp] = useState(false);
  const form = useForm<Form>({
    resolver: zodResolver(schema),
    defaultValues: { email: "", password: "", totp: "" },
  });
  const from = (location.state as { from?: string } | null)?.from ?? "/";
  if (user) navigate(from, { replace: true });

  const submit = form.handleSubmit(async (v) => {
    try {
      await login(v.email, v.password, v.totp);
      navigate(from, { replace: true });
    } catch (e) {
      if (e instanceof ApiError && (e.body as { totp_required?: boolean } | undefined)?.totp_required) {
        setNeedTotp(true);
        toast.info("Enter your two-factor code");
        return;
      }
      toast.error(e instanceof Error ? e.message : "Login failed");
    }
  });

  return (
    <AuthFrame title="Sign in to SuperSBC" description="Wholesale SBC administration">
      <form onSubmit={submit} className="space-y-3" noValidate>
        <Field label="Email" error={form.formState.errors.email?.message}>
          <Input
            type="email"
            autoComplete="username"
            autoFocus
            {...form.register("email")}
            data-testid="email"
          />
        </Field>
        <Field label="Password" error={form.formState.errors.password?.message}>
          <Input
            type="password"
            autoComplete="current-password"
            {...form.register("password")}
            data-testid="password"
          />
        </Field>
        {needTotp && (
          <Field label="Two-factor code">
            <Input
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="123456"
              {...form.register("totp")}
              data-testid="totp"
            />
          </Field>
        )}
        <Button
          type="submit"
          className="w-full"
          disabled={form.formState.isSubmitting}
          data-testid="login-submit"
        >
          Sign in
        </Button>
        <div className="text-center text-xs text-muted-foreground">
          <Link to="/forgot-password" className="underline">
            Forgot your password?
          </Link>
        </div>
      </form>
    </AuthFrame>
  );
}

export function ForgotPasswordPage() {
  const options = useQuery({
    queryKey: ["auth-options"],
    queryFn: () => get<{ email_reset: boolean }>("/auth/options"),
  });
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await post("/auth/forgot", { email });
      setSent(true);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Request failed");
    } finally {
      setBusy(false);
    }
  };
  if (options.data?.email_reset) {
    return (
      <AuthFrame title="Forgot password" description="We will e-mail you a one-time reset link">
        {sent ? (
          <p className="text-sm text-muted-foreground" data-testid="forgot-sent">
            If an account exists for <b>{email}</b>, a reset link is on its way. It is valid for one hour.
            Check your spam folder if it does not arrive.
          </p>
        ) : (
          <form onSubmit={submit} className="space-y-3" noValidate>
            <Field label="E-mail">
              <Input
                type="email"
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                data-testid="forgot-email"
              />
            </Field>
            <Button type="submit" className="w-full" disabled={busy || !email} data-testid="forgot-submit">
              Send reset link
            </Button>
          </form>
        )}
        <Button asChild variant="outline" className="mt-4 w-full">
          <Link to="/login">Back to sign in</Link>
        </Button>
      </AuthFrame>
    );
  }
  return (
    <AuthFrame title="Forgot password" description="E-mail delivery is not configured on this SBC">
      <p className="text-sm text-muted-foreground">
        Ask an administrator to generate a reset link for your account (Users and keys, Reset link). The link
        opens the reset page with a one-time token valid for one hour.
      </p>
      <Button asChild variant="outline" className="mt-4 w-full">
        <Link to="/login">Back to sign in</Link>
      </Button>
    </AuthFrame>
  );
}

const resetSchema = z
  .object({ password: z.string().min(10, "At least 10 characters"), confirm: z.string() })
  .refine((v) => v.password === v.confirm, { message: "Passwords differ", path: ["confirm"] });

export function ResetPasswordPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const token = params.get("token") ?? "";
  const form = useForm<z.infer<typeof resetSchema>>({
    resolver: zodResolver(resetSchema),
    defaultValues: { password: "", confirm: "" },
  });
  const submit = form.handleSubmit(async (v) => {
    try {
      await post("/auth/reset", { token, password: v.password });
      toast.success("Password updated, sign in again");
      navigate("/login");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Reset failed");
    }
  });
  return (
    <AuthFrame
      title="Reset password"
      description={token ? "Choose a new password" : "This link is missing its token"}
    >
      <form onSubmit={submit} className="space-y-3" noValidate>
        <Field label="New password" error={form.formState.errors.password?.message}>
          <Input type="password" autoComplete="new-password" {...form.register("password")} />
        </Field>
        <Field label="Confirm" error={form.formState.errors.confirm?.message}>
          <Input type="password" autoComplete="new-password" {...form.register("confirm")} />
        </Field>
        <Button type="submit" className="w-full" disabled={!token || form.formState.isSubmitting}>
          Set password
        </Button>
      </form>
    </AuthFrame>
  );
}
