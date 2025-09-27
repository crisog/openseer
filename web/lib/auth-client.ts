import { createAuthClient } from "better-auth/react";
import { organizationClient } from "better-auth/client/plugins";

export const authClient = createAuthClient({
  baseURL: process.env.NODE_ENV === "production" 
    ? process.env.BETTER_AUTH_URL 
    : "http://localhost:3000",
  plugins: [
    organizationClient(),
  ],
});

export const { 
  signIn, 
  signUp, 
  signOut, 
  useSession,
  getSession 
} = authClient;

export const organization = authClient.organization;
