import { createRouter } from '@tanstack/react-router'
import { setupRouterSsrQueryIntegration } from '@tanstack/react-router-ssr-query'
import type { QueryClient } from '@tanstack/react-query'
import { routeTree } from './routeTree.gen'
import { queryClient } from '@/lib/query-client'
import { DefaultCatchBoundary } from '@/components/boundaries/default-catch-boundary'
import { DefaultNotFound } from '@/components/boundaries/default-not-found'
import type { AuthQueryResult } from '@/lib/auth/queries'

interface RouterContext {
  queryClient: QueryClient
  user: AuthQueryResult | null
}

export function getRouter() {
  const router = createRouter({
    routeTree,
    context: { queryClient, user: null } satisfies RouterContext,
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
    defaultErrorComponent: DefaultCatchBoundary,
    defaultNotFoundComponent: DefaultNotFound,
    scrollRestoration: true,
    defaultStructuralSharing: true,
  })

  setupRouterSsrQueryIntegration({
    router,
    queryClient,
    handleRedirects: true,
    wrapQueryClient: true,
  })

  return router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>;
  }
}