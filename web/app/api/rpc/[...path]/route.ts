import { type NextRequest } from "next/server";

export async function POST(
  request: NextRequest,
  { params }: { params: Promise<{ path: string[] }> }
) {
  try {
    const controlPlaneUrl = process.env.CONTROL_PLANE_URL || "http://localhost:8082";

    const resolvedParams = await params;
    const servicePath = resolvedParams.path.join("/");
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
      if (key !== "set-cookie") {
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
}