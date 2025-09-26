import { createServerOnlyFn } from "@tanstack/react-start";
import { betterAuth } from "better-auth";
import { reactStartCookies } from "better-auth/react-start";
import { Pool } from "pg";

const getAuthConfig = createServerOnlyFn(() =>
  betterAuth({
    database: new Pool({
      connectionString: process.env.DATABASE_URL,
    }),
    advanced: {
      cookiePrefix: "openseer",
    },
    emailAndPassword: {
      enabled: true,
    },
    plugins: [
      reactStartCookies(),
    ],
  }),
);

export const auth = getAuthConfig();