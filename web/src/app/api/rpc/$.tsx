import { createFileRoute } from '@tanstack/react-router'
import { requireAuthMiddleware } from '@/lib/auth/middleware'

export const Route = createFileRoute('/api/rpc/$')({
  server: {
    middleware: [requireAuthMiddleware],
    handlers: {
      POST: async ({ request, params }) => {
        try {
          const controlPlaneUrl = process.env.CONTROL_PLANE_URL || "http://localhost:8082";

          // Get the splat parameter which contains the service path
          const servicePath = params['_splat'] || '';
          const body = await request.text();
          const cookieHeader = request.headers.get("cookie");

          const response = await fetch(`${controlPlaneUrl}/${servicePath}`, {
            method: "POST",
            headers: {
              "Content-Type": request.headers.get("content-type") || "application/json",
              ...(cookieHeader && { "cookie": cookieHeader }),
            },
            body,
          });

          const responseHeaders = new Headers();
          response.headers.forEach((value, key) => {
            if (key !== "set-cookie" && key !== "content-encoding" && key !== "content-length") {
              responseHeaders.set(key, value);
            }
          });

          const responseBody = await response.text();

          return new Response(responseBody, {
            status: response.status,
            statusText: response.statusText,
            headers: responseHeaders,
          });
        } catch (error) {
          console.error("RPC proxy error:", error);
          return Response.json(
            { error: "Internal server error" },
            { status: 500 }
          );
        }
      },
    },
  },
})