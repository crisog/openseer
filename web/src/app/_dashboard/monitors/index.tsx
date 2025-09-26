import { createFileRoute } from '@tanstack/react-router'
import { MonitorsContent } from '@/components/monitors/MonitorsContent'

export const Route = createFileRoute('/_dashboard/monitors/')({
  component: MonitorsListComponent,
})

function MonitorsListComponent() {
  return <MonitorsContent />
}