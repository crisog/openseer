import { betterAuth } from "better-auth";
import { nextCookies } from "better-auth/next-js";
import { Pool } from "pg";

export const auth = betterAuth({
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
    nextCookies(), 
  ],
});