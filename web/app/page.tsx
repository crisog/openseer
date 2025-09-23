"use client";

import React, { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { signIn, signUp, useSession } from "@/lib/auth-client";
import { useMutation } from "@connectrpc/connect-query";
import { UserService } from "@/lib/gen/openseer/v1/user_pb";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Monitor, Loader2, Eye, EyeOff } from "lucide-react";

export default function SignInPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [isSignUp, setIsSignUp] = useState(false);
  const [isAuthenticating, setIsAuthenticating] = useState(false);
  const [authError, setAuthError] = useState<string | null>(null);
  
  const router = useRouter();
  const session = useSession();
  const fetchUserProfile = useMutation(UserService.method.getUserProfile);

  useEffect(() => {
    if (session.data) {
      router.push("/dashboard");
    }
  }, [session.data, router]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsAuthenticating(true);
    setAuthError(null);

    try {
      let result;
      
      if (isSignUp) {
        result = await signUp.email({
          email,
          password,
          name,
        });
      } else {
        result = await signIn.email({
          email,
          password,
        });
      }

      if (result.error) {
        if (!isSignUp && (result.error.message?.includes("Invalid email or password") || 
                         result.error.message?.includes("User not found"))) {
          if (!name) {
            setIsSignUp(true);
            setAuthError("Account not found. Please enter your name to create a new account.");
            return;
          }
        } else {
          setAuthError(result.error.message || "Authentication failed");
        }
      } else {
        router.push("/dashboard");
      }
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : 'An unexpected error occurred';
      setAuthError(message);
    } finally {
      setIsAuthenticating(false);
    }
  };

  const isLoading = isAuthenticating || fetchUserProfile.isPending || session.isPending;
  const currentError = authError || fetchUserProfile.error?.message;

  if (session.isPending || session.data) {
    return (
      <div className="min-h-screen bg-background text-foreground flex items-center justify-center p-4">
        <Card className="surface w-full max-w-md">
          <CardHeader className="space-y-1">
            <CardTitle className="text-2xl text-center">
              {session.data ? "Redirecting..." : "Loading..."}
            </CardTitle>
            <p className="text-center text-muted-foreground">
              {session.data ? "Taking you to your dashboard" : "Please wait"}
            </p>
          </CardHeader>
          <CardContent className="flex justify-center">
            <Loader2 className="h-8 w-8 animate-spin" />
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        {/* Logo */}
        <div className="flex items-center justify-center space-x-3 mb-8">
          <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center">
            <Monitor className="w-6 h-6 text-primary-foreground" />
          </div>
          <span className="text-2xl font-bold">OpenSeer</span>
        </div>

        <Card className="surface">
          <CardHeader className="text-center space-y-3 py-6">
            <CardTitle className="text-2xl font-bold">
              {isSignUp ? "Create Account" : "Welcome back"}
            </CardTitle>
            <p className="text-muted-foreground">
              {isSignUp 
                ? "Complete your information to create your account" 
                : "Sign in to access your monitoring dashboard"
              }
            </p>
          </CardHeader>
          <CardContent className="space-y-8 px-8 pb-8">
            <form onSubmit={handleSubmit} className="space-y-6">
              {currentError && (
                <div className="rounded-lg bg-destructive/10 p-3 border border-destructive/20">
                  <p className="text-sm text-destructive">{currentError}</p>
                </div>
              )}
              
              {isSignUp && (
                <div className="space-y-2">
                  <Label htmlFor="name">Name</Label>
                  <Input
                    id="name"
                    name="name"
                    type="text"
                    autoComplete="name"
                    required={isSignUp}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Your full name"
                    disabled={isLoading}
                    className="h-11"
                  />
                </div>
              )}
              
              <div className="space-y-2">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  name="email"
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
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
                    onChange={(e) => setPassword(e.target.value)}
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
                    {showPassword ? (
                      <EyeOff className="h-4 w-4" />
                    ) : (
                      <Eye className="h-4 w-4" />
                    )}
                  </Button>
                </div>
              </div>

              <Button 
                type="submit" 
                disabled={isLoading} 
                size="lg"
                className="w-full h-12"
              >
                {isLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                {isAuthenticating
                  ? (isSignUp ? "Creating account..." : "Signing in...") 
                  : fetchUserProfile.isPending 
                    ? "Loading profile..."
                    : (isSignUp ? "Create Account" : "Sign In")
                }
              </Button>

              {isSignUp && (
                <div className="text-center">
                  <Button
                    type="button"
                    variant="link"
                    onClick={() => {
                      setIsSignUp(false);
                      setName("");
                      setAuthError(null);
                    }}
                    className="text-sm text-primary hover:underline"
                    disabled={isLoading}
                  >
                    Already have an account? Sign in
                  </Button>
                </div>
              )}
            </form>

            <div className="relative">
              <div className="absolute inset-0 flex items-center">
                <div className="w-full border-t border-border/40" />
              </div>
              <div className="relative flex justify-center text-xs uppercase">
                <span className="bg-background px-2 text-muted-foreground">Open Source Monitoring</span>
              </div>
            </div>

          </CardContent>
        </Card>
      </div>
    </div>
  );
}