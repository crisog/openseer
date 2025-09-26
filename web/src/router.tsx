import { createRouter } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'
import { queryClient } from '@/lib/query-client'

export function getRouter() {
  const router = createRouter({
    routeTree,
    scrollRestoration: true,
    context: {
      queryClient,
    },
  })

  return router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createRouter>;
  }
}