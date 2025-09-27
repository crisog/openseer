import { Pool } from "pg";

declare global {
  var pgPool: Pool | undefined;
}

const connectionString = process.env.DATABASE_URL;

if (!connectionString) {
  throw new Error("DATABASE_URL environment variable is not set");
}

export const pgPool = globalThis.pgPool ?? new Pool({
  connectionString,
});

if (process.env.NODE_ENV !== "production") {
  globalThis.pgPool = pgPool;
}

export default pgPool;
