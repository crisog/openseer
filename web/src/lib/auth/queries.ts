import { queryOptions } from '@tanstack/react-query'
import { getSession } from '@/lib/auth-client'

export const AUTH_SESSION_QUERY_KEY = ['auth', 'session'] as const

export const authQueryOptions = () =>
  queryOptions({
    queryKey: AUTH_SESSION_QUERY_KEY,
    queryFn: async () => {
      try {
        const { data } = await getSession()
        return data ?? null
      } catch (error) {
        console.warn('Failed to get session:', error)
        return null
      }
    },
    staleTime: 1000 * 60 * 5, // 5 minutes
    retry: (failureCount, error: any) => {
      // Don't retry on auth errors
      if (error?.status === 401 || error?.status === 403) {
        return false
      }
      return failureCount < 2
    },
  })

export type AuthQueryResult = Awaited<ReturnType<typeof getSession>>['data']
