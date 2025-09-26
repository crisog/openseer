import { createFileRoute, Outlet } from '@tanstack/react-router'

export const Route = createFileRoute('/_dashboard/monitors')({
  component: MonitorsLayoutComponent,
})

function MonitorsLayoutComponent() {
  return <Outlet />
}