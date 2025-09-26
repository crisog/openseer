import { createFileRoute, Outlet } from '@tanstack/react-router'
import { MainLayout } from '@/components/MainLayout'
import { requireAuthMiddleware } from '@/lib/auth/middleware'

export const Route = createFileRoute('/_dashboard')({
  server: {
    middleware: [requireAuthMiddleware],
  },
  component: DashboardLayout,
})

function DashboardLayout() {
  return (
    <MainLayout>
      <Outlet />
    </MainLayout>
  )
}