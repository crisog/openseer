import { createMiddleware } from "@tanstack/react-start";
import { getRequest, setResponseStatus } from "@tanstack/react-start/server";
import { redirect } from "@tanstack/react-router";
import { auth } from "@/lib/auth";

/**
 * Middleware to handle authentication and redirects for protected routes
 */
export const authMiddleware = createMiddleware().server(async ({ next }) => {
  const request = getRequest();
  const url = new URL(request.url);
  const pathname = url.pathname;

  try {
    const session = await auth.api.getSession({
      headers: request.headers,
      query: {
        // ensure session is fresh
        disableCookieCache: true,
      },
    });

    const isAuthPage = pathname === "/" ||
                      pathname === "/sign-in" ||
                      pathname === "/login";
    const isDashboard = pathname.startsWith("/dashboard") || pathname.startsWith("/monitors");

    // If user is authenticated and tries to access auth pages, redirect to dashboard
    if (session && isAuthPage) {
      throw redirect({
        to: "/dashboard"
      });
    }

    // If user is not authenticated and tries to access protected pages, redirect to login
    if (!session && isDashboard) {
      throw redirect({
        to: "/"
      });
    }

    // Add user to context for authenticated requests
    return next({
      context: session ? { user: session.user, session } : {}
    });
  } catch (error) {
    console.error('Auth middleware error:', error);
    return next();
  }
});

/**
 * Middleware to force authentication on server requests (including server functions), and add the user to the context.
 */
export const requireAuthMiddleware = createMiddleware().server(async ({ next }) => {
  const session = await auth.api.getSession({
    headers: getRequest().headers,
    query: {
      // ensure session is fresh
      disableCookieCache: true,
    },
  });

  if (!session) {
    setResponseStatus(401);
    throw new Error("Unauthorized");
  }

  return next({ context: { user: session.user, session } });
});