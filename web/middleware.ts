import { NextRequest, NextResponse } from "next/server";
import { headers } from "next/headers";
import { auth } from "@/lib/auth";

export async function middleware(request: NextRequest) {
    const session = await auth.api.getSession({
        headers: await headers()
    });

    const isAuthPage = request.nextUrl.pathname === "/" || 
                      request.nextUrl.pathname === "/sign-in" ||
                      request.nextUrl.pathname === "/login";
    const isDashboard = request.nextUrl.pathname.startsWith("/dashboard");

    if (session && isAuthPage) {
        return NextResponse.redirect(new URL("/dashboard", request.url));
    }

    if (!session && isDashboard) {
        return NextResponse.redirect(new URL("/", request.url));
    }

    return NextResponse.next();
}

export const config = {
    runtime: "nodejs",
    matcher: ["/dashboard/:path*"],
};