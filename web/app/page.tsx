"use client";

import React, { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { LoadingScreen } from "@/components/auth/loading-screen";
import { OnboardingWizard } from "@/components/auth/onboarding-wizard";
import { SignInPanel } from "@/components/auth/sign-in-panel";
import { useSession } from "@/lib/auth-client";

type OnboardingState = "loading" | "setup" | "ready";

export default function SignInPage() {
  const router = useRouter();
  const session = useSession();
  const [onboardingState, setOnboardingState] = useState<OnboardingState>("loading");
  const [statusMessage, setStatusMessage] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    const fetchOnboardingStatus = async () => {
      try {
        const response = await fetch("/api/onboarding", { cache: "no-store" });
        if (!response.ok) {
          throw new Error("Request failed");
        }
        const data: { needsSetup: boolean } = await response.json();

        if (!cancelled) {
          setOnboardingState(data.needsSetup ? "setup" : "ready");
        }
      } catch (error) {
        if (!cancelled) {
          setOnboardingState("ready");
          setStatusMessage(
            "Could not automatically verify setup status. If this is a fresh deployment, refresh and try again."
          );
        }
      }
    };

    fetchOnboardingStatus();

    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (session.data) {
      router.push("/dashboard");
    }
  }, [session.data, router]);

  if (session.isPending || session.data) {
    return <LoadingScreen title="Loading" message="Preparing your dashboard" />;
  }

  if (onboardingState === "loading") {
    return <LoadingScreen title="Checking setup" message="Inspecting instance state" />;
  }

  if (onboardingState === "setup") {
    return <OnboardingWizard onComplete={() => setOnboardingState("ready")} />;
  }

  return <SignInPanel statusMessage={statusMessage} />;
}
