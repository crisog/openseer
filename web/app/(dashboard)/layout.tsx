import { MainLayout } from "@/components/MainLayout"
import type { Metadata } from "next"

export const metadata: Metadata = {
  title: "Dashboard | OpenSeer",
}

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode
}) {
  return <MainLayout>{children}</MainLayout>
}


