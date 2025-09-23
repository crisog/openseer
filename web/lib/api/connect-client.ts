import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { UserService } from "@/lib/gen/openseer/v1/user_pb";
import { ChecksService } from "@/lib/gen/openseer/v1/checks_pb";
import { DashboardService } from "@/lib/gen/openseer/v1/dashboard_pb";
import { EnrollmentService } from "@/lib/gen/openseer/v1/enrollment_pb";
import { HealthService } from "@/lib/gen/openseer/v1/health_pb";
import { MonitorsService } from "@/lib/gen/openseer/v1/monitors_pb";

export const transport = createConnectTransport({
  baseUrl: process.env.NEXT_PUBLIC_CONTROL_PLANE_URL || "https://localhost:8082",
  fetch: (input, init) => fetch(input, { ...init, credentials: "include" }),
});

export const userClient = createClient(UserService, transport);
export const checksClient = createClient(ChecksService, transport);
export const dashboardClient = createClient(DashboardService, transport);
export const enrollmentClient = createClient(EnrollmentService, transport);
export const healthClient = createClient(HealthService, transport);
export const monitorsClient = createClient(MonitorsService, transport);