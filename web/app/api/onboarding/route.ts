import { DatabaseError } from "pg";
import { NextResponse } from "next/server";

import { pgPool } from "@/lib/db";

async function countRows(tableName: string): Promise<number> {
  try {
    const result = await pgPool.query<{ count: string }>(
      `select count(*)::text as count from ${tableName}`
    );
    return Number(result.rows[0]?.count ?? "0");
  } catch (error) {
    if (error instanceof DatabaseError && error.code === "42P01") {
      // Table missing (probably migrations not applied yet) – treat as empty for onboarding.
      return 0;
    }
    throw error;
  }
}

export async function GET() {
  try {
    const [userCount, organizationCount] = await Promise.all([
      countRows('"user"'),
      countRows('"organization"'),
    ]);

    return NextResponse.json({
      needsSetup: userCount === 0,
      userCount,
      organizationCount,
    });
  } catch (error) {
    console.error("Failed to fetch onboarding status", error);
    return NextResponse.json(
      {
        error: "Failed to determine onboarding status",
      },
      { status: 500 }
    );
  }
}
