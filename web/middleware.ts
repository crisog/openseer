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

    const protectedPrefixes = [
        "/monitors",
        "/settings",
        "/incidents",
        "/analytics",
    ];

    const isProtectedRoute = protectedPrefixes.some((prefix) =>
        request.nextUrl.pathname === prefix || request.nextUrl.pathname.startsWith(`${prefix}/`)
    );

    if (session && isAuthPage) {
        return NextResponse.redirect(new URL("/monitors", request.url));
    }

    if (!session && isProtectedRoute) {
        return NextResponse.redirect(new URL("/", request.url));
    }

    return NextResponse.next();
}

export const config = {
    runtime: "nodejs",
    matcher: [
        "/monitors/:path*",
        "/settings/:path*",
        "/incidents/:path*",
        "/analytics/:path*"
    ],
};