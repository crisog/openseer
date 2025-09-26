import { queryOptions } from '@tanstack/react-query'
import { getSession } from '@/lib/auth-client'

export const authQueryOptions = () =>
  queryOptions({
    queryKey: ['auth', 'session'],
    queryFn: async () => {
      try {
        const session = await getSession()
        return session
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

export type AuthQueryResult = Awaited<ReturnType<typeof getSession>>