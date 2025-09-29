"use client";

import React, { useEffect } from "react";
import { useRouter } from "next/navigation";

import { LoadingScreen } from "@/components/auth/loading-screen";
import { SignInPanel } from "@/components/auth/sign-in-panel";
import { useSession } from "@/lib/auth-client";

export default function SignInPage() {
  const router = useRouter();
  const session = useSession();

  useEffect(() => {
    if (session.data) {
      router.push("/monitors");
    }
  }, [session.data, router]);

  if (session.isPending || session.data) {
    return <LoadingScreen title="Loading" message="Preparing your workspace" />;
  }

  return <SignInPanel />;
}
