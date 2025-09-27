import { betterAuth } from "better-auth";
import { nextCookies } from "better-auth/next-js";
import { organization } from "better-auth/plugins";
import { pgPool } from "./db";

export const auth = betterAuth({
  database: pgPool,
  advanced: {
    cookiePrefix: "openseer",
  },
  emailAndPassword: {
    enabled: true,
  },
  plugins: [
    nextCookies(),
    organization({
      allowUserToCreateOrganization: true,
      membershipLimit: 100,
    }),
  ],
});
