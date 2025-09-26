import { createAuthClient } from "better-auth/react";

const authClient = createAuthClient({
  baseURL: process.env.NODE_ENV === "production"
    ? process.env.BETTER_AUTH_URL
    : "http://localhost:3000"
});

export default authClient;

export const {
  signIn,
  signUp,
  signOut,
  useSession,
  getSession
} = authClient;