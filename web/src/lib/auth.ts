import { betterAuth } from "better-auth";
import { reactStartCookies } from "better-auth/react-start";
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
    reactStartCookies(), // make sure this is the last plugin in the array
  ],
});