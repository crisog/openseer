import { createFileRoute, redirect } from '@tanstack/react-router'

export const Route = createFileRoute('/_dashboard/dashboard')({
  beforeLoad: () => {
    throw redirect({
      to: '/monitors'
    })
  }
})