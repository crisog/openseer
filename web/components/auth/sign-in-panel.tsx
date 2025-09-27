"use client";

import React, { useState } from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle, Eye, EyeOff, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { signIn } from "@/lib/auth-client";

import { BrandHeader } from "./brand-header";

type SignInPanelProps = {
  statusMessage?: string | null;
};

export function SignInPanel({ statusMessage }: SignInPanelProps) {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [isAuthenticating, setIsAuthenticating] = useState(false);
  const [authError, setAuthError] = useState<string | null>(null);

  const handleSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setIsAuthenticating(true);
    setAuthError(null);

    try {
      const result = await signIn.email({
        email,
        password,
      });

      if (result.error) {
        const message =
          result.error.message && result.error.message !== ""
            ? result.error.message
            : "Invalid email or password";
        setAuthError(message);
      } else {
        router.push("/dashboard");
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "Authentication failed";
      setAuthError(message);
    } finally {
      setIsAuthenticating(false);
    }
  };

  const isLoading = isAuthenticating;

  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        <BrandHeader />
        <Card className="surface">
          <CardHeader className="text-center space-y-3 py-6">
            <CardTitle className="text-2xl font-bold">Welcome back</CardTitle>
            <p className="text-muted-foreground">
              Sign in with the administrator credentials that were provisioned for you
            </p>
          </CardHeader>
          <CardContent className="space-y-6 px-8 pb-8">
            {statusMessage ? (
              <div className="rounded-lg bg-muted/50 p-3 text-sm text-muted-foreground flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 mt-0.5" />
                <span>{statusMessage}</span>
              </div>
            ) : null}

            {authError ? (
              <div className="rounded-lg bg-destructive/10 p-3 border border-destructive/20">
                <p className="text-sm text-destructive">{authError}</p>
              </div>
            ) : null}

            <form onSubmit={handleSubmit} className="space-y-6">
              <div className="space-y-2">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  name="email"
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  placeholder="you@email.com"
                  disabled={isLoading}
                  className="h-11"
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="password">Password</Label>
                <div className="relative">
                  <Input
                    id="password"
                    name="password"
                    type={showPassword ? "text" : "password"}
                    autoComplete="current-password"
                    required
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    placeholder="Your password"
                    disabled={isLoading}
                    className="h-11 pr-10"
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="absolute right-0 top-0 h-full px-3 py-2 hover:bg-transparent"
                    onClick={() => setShowPassword(!showPassword)}
                    disabled={isLoading}
                  >
                    {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                  </Button>
                </div>
              </div>

              <Button type="submit" disabled={isLoading} size="lg" className="w-full h-12">
                {isLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                {isLoading ? "Signing in..." : "Sign In"}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
