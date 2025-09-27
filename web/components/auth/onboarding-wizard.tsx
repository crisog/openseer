"use client";

import React, { Fragment, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Check, Eye, EyeOff, Loader2 } from "lucide-react";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { organization, signUp } from "@/lib/auth-client";
import { slugify } from "@/lib/utils";

import { BrandHeader } from "./brand-header";

type OnboardingWizardProps = {
  onComplete: () => void;
};

type ClientActionResult<T> = {
  data?: T;
  error?: {
    message?: string;
  };
};

type CreatedOrganization = {
  id: string;
  slug?: string | null;
};

const onboardingSchema = z
  .object({
    name: z.string().min(1, "Please provide your name"),
    email: z.string().email("Enter a valid email"),
    password: z.string().min(8, "Password must be at least 8 characters"),
    confirmPassword: z.string().min(1, "Confirm your password"),
    organizationName: z.string().min(1, "Organization name is required"),
    organizationSlug: z
      .string()
      .min(1, "Organization slug is required")
      .regex(/^[a-z0-9-]+$/, "Slug can contain lowercase letters, numbers, and hyphens"),
  })
  .refine((data) => data.password === data.confirmPassword, {
    message: "Passwords do not match",
    path: ["confirmPassword"],
  });

type OnboardingFormValues = z.infer<typeof onboardingSchema>;

type Step = "account" | "organization";

type StepConfig = {
  id: Step;
  title: string;
  description: string;
};

const STEPS: StepConfig[] = [
  {
    id: "account",
    title: "Administrator",
    description: "Root access",
  },
  {
    id: "organization",
    title: "Organization",
    description: "Workspace details",
  },
];

function StepIndicator({ currentStep, showLabels = false }: { currentStep: Step; showLabels?: boolean }) {
  const currentIndex = STEPS.findIndex((step) => step.id === currentStep);

  return (
    <div className="flex flex-wrap items-center justify-center gap-4 sm:gap-6">
      {STEPS.map((step, index) => {
        const status =
          index < currentIndex ? "completed" : index === currentIndex ? "current" : "upcoming";
        const circleClasses =
          status === "completed"
            ? "bg-primary text-primary-foreground border-primary"
            : status === "current"
            ? "border-primary text-primary"
            : "border-border text-muted-foreground";

        return (
          <Fragment key={step.id}>
            <div className="flex flex-col items-center gap-2 sm:flex-row sm:items-center sm:gap-3">
              <div
                className={`flex h-10 w-10 items-center justify-center rounded-full border text-sm font-medium transition-colors ${circleClasses}`}
                aria-current={status === "current"}
              >
                {status === "completed" ? <Check className="h-4 w-4" /> : index + 1}
              </div>
              {showLabels ? (
                <div className="hidden sm:flex sm:flex-col sm:items-start">
                  <span
                    className={`text-sm font-medium ${
                      status === "current" ? "text-foreground" : "text-muted-foreground"
                    }`}
                  >
                    {step.title}
                  </span>
                  <span className="text-xs text-muted-foreground">{step.description}</span>
                </div>
              ) : null}
            </div>
            {index < STEPS.length - 1 ? (
              <div
                className={`h-px w-10 sm:w-16 ${
                  index < currentIndex ? "bg-primary" : "bg-border"
                }`}
              />
            ) : null}
          </Fragment>
        );
      })}
    </div>
  );
}

export function OnboardingWizard({ onComplete }: OnboardingWizardProps) {
  const router = useRouter();
  const [currentStep, setCurrentStep] = useState<Step>("account");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showPassword, setShowPassword] = useState(false);
  const [slugManuallyEdited, setSlugManuallyEdited] = useState(false);

  const form = useForm<OnboardingFormValues>({
    resolver: zodResolver(onboardingSchema),
    defaultValues: {
      name: "",
      email: "",
      password: "",
      confirmPassword: "",
      organizationName: "",
      organizationSlug: "",
    },
    mode: "onTouched",
  });

  const organizationName = form.watch("organizationName");

  useEffect(() => {
    if (!slugManuallyEdited) {
      const nextSlug = slugify(organizationName);
      form.setValue("organizationSlug", nextSlug, {
        shouldDirty: false,
        shouldTouch: false,
        shouldValidate: currentStep === "organization",
      });
    }
  }, [organizationName, slugManuallyEdited, currentStep, form]);

  const accountFields: Array<keyof OnboardingFormValues> = useMemo(
    () => ["name", "email", "password", "confirmPassword"],
    []
  );

  const handleGoToOrganization = async () => {
    const valid = await form.trigger(accountFields);
    if (!valid) return;
    setCurrentStep("organization");
  };

  const handleBackToAccount = () => {
    setCurrentStep("account");
  };

  const handleComplete = form.handleSubmit(async (values) => {
    setError(null);
    setIsSubmitting(true);

    try {
      const signUpResult = await signUp.email({
        email: values.email.trim().toLowerCase(),
        password: values.password,
        name: values.name.trim(),
      });

      if (signUpResult.error) {
        const message =
          signUpResult.error.message && signUpResult.error.message !== ""
            ? signUpResult.error.message
            : "Failed to create administrator account";
        setError(message);
        return;
      }

      const organizationResult = (await organization.create({
        name: values.organizationName.trim(),
        slug: values.organizationSlug.trim(),
      })) as unknown as ClientActionResult<CreatedOrganization>;

      if (organizationResult.error) {
        const message =
          organizationResult.error.message && organizationResult.error.message !== ""
            ? organizationResult.error.message
            : "Failed to create organization";
        setError(message);
        return;
      }

      const createdOrg = organizationResult.data;

      if (createdOrg?.id) {
        await organization.setActive({
          organizationId: createdOrg.id,
          organizationSlug: createdOrg.slug ?? values.organizationSlug.trim(),
        });
      }

      onComplete();
      router.push("/dashboard");
    } catch (submitError) {
      const message =
        submitError instanceof Error ? submitError.message : "Failed to complete setup";
      setError(message);
    } finally {
      setIsSubmitting(false);
    }
  });

  const isAccountStep = currentStep === "account";

  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center p-4">
      <div className="w-full max-w-xl">
        <BrandHeader />
        <Card className="surface mx-auto w-full max-w-xl">
          <div className="space-y-5 px-6 pt-6 sm:px-8">
            <StepIndicator currentStep={currentStep} showLabels />
          </div>
          <CardContent className="space-y-6 px-6 pb-6 sm:px-8">

            {error ? (
              <div className="rounded-lg bg-destructive/10 p-3 border border-destructive/20">
                <p className="text-sm text-destructive">{error}</p>
              </div>
            ) : null}

            <form className="space-y-6">
              {isAccountStep ? (
                <div className="grid gap-6 md:grid-cols-2">
                  <div className="space-y-2">
                    <Label htmlFor="admin-name">Name</Label>
                    <Input
                      id="admin-name"
                      type="text"
                      autoComplete="name"
                      placeholder="Jane Doe"
                      disabled={isSubmitting}
                      {...form.register("name")}
                    />
                    {form.formState.errors.name ? (
                      <p className="text-sm text-destructive">
                        {form.formState.errors.name.message}
                      </p>
                    ) : null}
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="admin-email">Email</Label>
                    <Input
                      id="admin-email"
                      type="email"
                      autoComplete="email"
                      placeholder="admin@example.com"
                      disabled={isSubmitting}
                      {...form.register("email")}
                    />
                    {form.formState.errors.email ? (
                      <p className="text-sm text-destructive">
                        {form.formState.errors.email.message}
                      </p>
                    ) : null}
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="admin-password">Password</Label>
                    <div className="relative">
                      <Input
                        id="admin-password"
                        type={showPassword ? "text" : "password"}
                        autoComplete="new-password"
                        placeholder="Strong password"
                        disabled={isSubmitting}
                        {...form.register("password")}
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="absolute right-0 top-0 h-full px-3 py-2 hover:bg-transparent"
                        onClick={() => setShowPassword((prev) => !prev)}
                        disabled={isSubmitting}
                      >
                        {showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      </Button>
                    </div>
                    {form.formState.errors.password ? (
                      <p className="text-sm text-destructive">
                        {form.formState.errors.password.message}
                      </p>
                    ) : null}
                  </div>

                  <div className="space-y-2">
                    <Label htmlFor="admin-password-confirm">Confirm password</Label>
                    <Input
                      id="admin-password-confirm"
                      type="password"
                      autoComplete="new-password"
                      placeholder="Repeat password"
                      disabled={isSubmitting}
                      {...form.register("confirmPassword")}
                    />
                    {form.formState.errors.confirmPassword ? (
                      <p className="text-sm text-destructive">
                        {form.formState.errors.confirmPassword.message}
                      </p>
                    ) : null}
                  </div>
                </div>
              ) : (
                <div className="grid gap-6 md:grid-cols-2">
                  <div className="space-y-2 md:col-span-2">
                    <Label htmlFor="organization-name">Organization name</Label>
                    <Input
                      id="organization-name"
                      type="text"
                      placeholder="OpenSeer HQ"
                      disabled={isSubmitting}
                      {...form.register("organizationName")}
                    />
                    {form.formState.errors.organizationName ? (
                      <p className="text-sm text-destructive">
                        {form.formState.errors.organizationName.message}
                      </p>
                    ) : null}
                  </div>
                  <div className="space-y-2 md:col-span-2">
                    <Label htmlFor="organization-slug">Organization slug</Label>
                    <Input
                      id="organization-slug"
                      type="text"
                      placeholder="openseer"
                      disabled={isSubmitting}
                      {...form.register("organizationSlug", {
                        onChange: () => setSlugManuallyEdited(true),
                      })}
                    />
                    <div className="flex flex-col md:flex-row md:items-center md:justify-between gap-1 text-xs text-muted-foreground">
                      <span>Lowercase letters, numbers, and hyphens only.</span>
                    </div>
                    {form.formState.errors.organizationSlug ? (
                      <p className="text-sm text-destructive">
                        {form.formState.errors.organizationSlug.message}
                      </p>
                    ) : null}
                  </div>
                </div>
              )}
              <div className="flex flex-col items-center gap-3 sm:flex-row sm:justify-center">
                {isAccountStep ? null : (
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={handleBackToAccount}
                    disabled={isSubmitting}
                  >
                    Back
                  </Button>
                )}

                {isAccountStep ? (
                  <Button type="button" onClick={handleGoToOrganization}>
                    Continue
                  </Button>
                ) : (
                  <Button type="button" onClick={handleComplete} disabled={isSubmitting}>
                    {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                    {isSubmitting ? "Setting up..." : "Create Admin & Organization"}
                  </Button>
                )}
              </div>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
