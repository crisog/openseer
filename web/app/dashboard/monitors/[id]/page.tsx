"use client"

import { useMemo, useState } from "react"
import { useRouter, useParams } from "next/navigation"
import { useSession } from "@/lib/auth-client"
import { ArrowLeft, Clock, Globe, Activity, AlertCircle, CheckCircle, XCircle, Loader2, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useQuery } from "@connectrpc/connect-query"
import { MonitorsService } from "@/lib/gen/openseer/v1/monitors_pb"
import type { Monitor, MonitorResult as PbMonitorResult, MonitorMetrics as PbMonitorMetrics } from "@/lib/gen/openseer/v1/monitors_pb"
import { LatencyChart } from "@/components/monitors/LatencyChart"
import { UptimeChart } from "@/components/monitors/UptimeChart"

interface UiMonitor {
  id: string
  name: string
  url: string
  interval_ms: number
  timeout_ms: number
  method: string
  enabled: boolean
  created_at: string
  updated_at: string
  regions: string[]
}

interface MonitorResult {
  run_id: string
  monitor_id: string
  region: string
  event_at: string
  status: string
  http_code?: number
  dns_ms?: number
  connect_ms?: number
  tls_ms?: number
  ttfb_ms?: number
  download_ms?: number
  total_ms?: number
  size_bytes?: number
  error_message?: string
}

interface MonitorMetric {
  monitor_id: string
  region: string
  bucket: string
  count: number
  error_count: number
  error_rate: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
  min_ms: number
  max_ms: number
  avg_ms: number
}

export default function MonitorDetailsPage() {
  const router = useRouter()
  const params = useParams()
  const session = useSession()
  const monitorId = params.id as string
  
  const [refreshing, setRefreshing] = useState(false)
  const detailsQuery = useQuery(
    MonitorsService.method.getMonitor,
    { id: monitorId },
    { enabled: !!monitorId }
  )

  const resultsQuery = useQuery(
    MonitorsService.method.getMonitorResults,
    { monitorId, limit: 100 },
    { enabled: !!monitorId }
  )

  const metricsQuery = useQuery(
    MonitorsService.method.getMonitorMetrics,
    { monitorId },
    { enabled: !!monitorId }
  )

  const monitor: UiMonitor | null = useMemo(() => {
    const m = detailsQuery.data?.monitor as Monitor | undefined
    if (!m) return null
    return {
      id: m.id,
      name: m.name,
      url: m.url,
      interval_ms: m.intervalMs,
      timeout_ms: m.timeoutMs,
      method: m.method,
      enabled: m.enabled ?? false,
      created_at: m.createdAt ? new Date(Number(m.createdAt.seconds) * 1000).toISOString() : "",
      updated_at: m.updatedAt ? new Date(Number(m.updatedAt.seconds) * 1000).toISOString() : "",
      regions: m.regions ?? [],
    }
  }, [detailsQuery.data])

  const results: MonitorResult[] = useMemo(() => {
    return (resultsQuery.data?.results ?? []).map((r: PbMonitorResult) => ({
      run_id: r.runId,
      monitor_id: r.monitorId,
      region: r.region,
      event_at: r.eventAt ? new Date(Number(r.eventAt.seconds) * 1000).toISOString() : "",
      status: r.status,
      http_code: r.httpCode,
      dns_ms: r.dnsMs,
      connect_ms: r.connectMs,
      tls_ms: r.tlsMs,
      ttfb_ms: r.ttfbMs,
      download_ms: r.downloadMs,
      total_ms: r.totalMs,
      size_bytes: r.sizeBytes ? Number(r.sizeBytes) : undefined,
      error_message: r.errorMessage,
    }))
  }, [resultsQuery.data])

  const metrics: MonitorMetric[] = useMemo(() => {
    return (metricsQuery.data?.metrics ?? []).map((m: PbMonitorMetrics) => ({
      monitor_id: m.monitorId,
      region: m.region,
      bucket: m.bucket ? new Date(Number(m.bucket.seconds) * 1000).toISOString() : "",
      count: Number(m.count),
      error_count: Number(m.errorCount),
      error_rate: m.errorRate,
      p50_ms: m.p50Ms,
      p95_ms: m.p95Ms,
      p99_ms: m.p99Ms,
      min_ms: m.minMs,
      max_ms: m.maxMs,
      avg_ms: m.avgMs,
    }))
  }, [metricsQuery.data])

  const refreshData = async () => {
    setRefreshing(true)
    await Promise.all([
      detailsQuery.refetch(),
      resultsQuery.refetch(),
      metricsQuery.refetch(),
    ])
    setRefreshing(false)
  }

  const getStatusIcon = (status: string) => {
    switch (status) {
      case "OK":
        return <CheckCircle className="h-4 w-4 text-green-500" />
      case "FAIL":
        return <XCircle className="h-4 w-4 text-red-500" />
      case "ERROR":
        return <AlertCircle className="h-4 w-4 text-yellow-500" />
      default:
        return <Clock className="h-4 w-4 text-gray-500" />
    }
  }

  const getStatusBadgeVariant = (status: string): "default" | "destructive" | "secondary" | "outline" => {
    switch (status) {
      case "OK":
        return "default"
      case "FAIL":
        return "destructive"
      case "ERROR":
        return "secondary"
      default:
        return "outline"
    }
  }

  const calculateUptime = () => {
    if (results.length === 0) return 100
    const successCount = results.filter(r => r.status === "OK").length
    return ((successCount / results.length) * 100).toFixed(2)
  }

  const calculateAvgResponseTime = () => {
    const validResults = results.filter(r => r.total_ms)
    if (validResults.length === 0) return 0
    const sum = validResults.reduce((acc, r) => acc + (r.total_ms || 0), 0)
    return Math.round(sum / validResults.length)
  }

  const loading = detailsQuery.isPending || resultsQuery.isPending || metricsQuery.isPending
  if (session.isPending || loading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <Loader2 className="h-8 w-8 animate-spin" />
      </div>
    )
  }

  if (!session.data) {
    return null
  }

  if (!monitor) {
    return (
      <div className="container mx-auto py-8 px-4">
        <div className="text-center">
          <h2 className="text-2xl font-bold mb-4">Monitor not found</h2>
          <Button 
            onClick={() => router.push("/dashboard")}
            size="sm"
          >
            <ArrowLeft className="h-4 w-4 mr-2" />
            Back to Dashboard
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="container mx-auto py-8 px-4">
        {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div className="flex items-center gap-4">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => router.push("/dashboard")}
          >
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <div>
            <h1 className="text-3xl font-bold">{monitor.name || monitor.url}</h1>
            <p className="text-muted-foreground mt-1">
              {monitor.url} • {monitor.method} • Every {monitor.interval_ms / 1000}s • Timeout {monitor.timeout_ms / 1000}s
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={monitor.enabled ? "default" : "secondary"}>
            {monitor.enabled ? "Active" : "Disabled"}
          </Badge>
          <Button
            variant="outline"
            size="sm"
            onClick={refreshData}
            disabled={refreshing}
            className="h-7 px-2"
          >
            {refreshing ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            <span className="ml-1">Refresh</span>
          </Button>
        </div>
      </div>

      {/* Stats Cards */}
      <div className="grid gap-4 md:grid-cols-4 mb-8">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Uptime</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{calculateUptime()}%</div>
            <p className="text-xs text-muted-foreground">
              Last {results.length} checks
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Avg Response</CardTitle>
            <Clock className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{calculateAvgResponseTime()} ms</div>
            <p className="text-xs text-muted-foreground">
              Average latency
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Total Checks</CardTitle>
            <Globe className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{results.length}</div>
            <p className="text-xs text-muted-foreground">
              In current view
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">Failed Checks</CardTitle>
            <AlertCircle className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">
              {results.filter(r => r.status !== "OK").length}
            </div>
            <p className="text-xs text-muted-foreground">
              Errors & failures
            </p>
          </CardContent>
        </Card>
      </div>

      {/* Main Content Tabs */}
      <Tabs defaultValue="metrics" className="space-y-4">
        <TabsList>
          <TabsTrigger value="metrics">Metrics</TabsTrigger>
          <TabsTrigger value="results">Recent Results</TabsTrigger>
          <TabsTrigger value="timings">Response Timings</TabsTrigger>
        </TabsList>

        {/* Metrics Tab */}
        <TabsContent value="metrics" className="space-y-4">
          <div className="grid gap-6 lg:grid-cols-2">
            <LatencyChart monitorId={monitorId} />
            <UptimeChart monitorId={monitorId} />
          </div>
        </TabsContent>

        {/* Results Tab */}
        <TabsContent value="results">
          <Card>
            <CardHeader>
              <CardTitle>Recent Monitor Results</CardTitle>
              <CardDescription>
                Last {results.length} monitor executions
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Status</TableHead>
                    <TableHead>Timestamp</TableHead>
                    <TableHead>Region</TableHead>
                    <TableHead>HTTP Code</TableHead>
                    <TableHead>Response Time</TableHead>
                    <TableHead>Error</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {results.map((result) => (
                    <TableRow key={result.run_id}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          {getStatusIcon(result.status)}
                          <Badge variant={getStatusBadgeVariant(result.status)}>
                            {result.status}
                          </Badge>
                        </div>
                      </TableCell>
                      <TableCell>
                        {new Date(result.event_at).toLocaleString()}
                      </TableCell>
                      <TableCell>{result.region}</TableCell>
                      <TableCell>
                        {result.http_code || "-"}
                      </TableCell>
                      <TableCell>
                        {result.total_ms ? `${result.total_ms} ms` : "-"}
                      </TableCell>
                      <TableCell className="max-w-xs truncate">
                        {result.error_message || "-"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>

        {/* Response Timings Tab */}
        <TabsContent value="timings">
          <Card>
            <CardHeader>
              <CardTitle>Response Time Breakdown</CardTitle>
              <CardDescription>
                Detailed timing metrics for recent checks
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Timestamp</TableHead>
                    <TableHead>DNS</TableHead>
                    <TableHead>Connect</TableHead>
                    <TableHead>TLS</TableHead>
                    <TableHead>TTFB</TableHead>
                    <TableHead>Download</TableHead>
                    <TableHead>Total</TableHead>
                    <TableHead>Size</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {results.filter(r => r.status === "OK").map((result) => (
                    <TableRow key={result.run_id}>
                      <TableCell>
                        {new Date(result.event_at).toLocaleString()}
                      </TableCell>
                      <TableCell>
                        {result.dns_ms ? `${result.dns_ms} ms` : "-"}
                      </TableCell>
                      <TableCell>
                        {result.connect_ms ? `${result.connect_ms} ms` : "-"}
                      </TableCell>
                      <TableCell>
                        {result.tls_ms ? `${result.tls_ms} ms` : "-"}
                      </TableCell>
                      <TableCell>
                        {result.ttfb_ms ? `${result.ttfb_ms} ms` : "-"}
                      </TableCell>
                      <TableCell>
                        {result.download_ms ? `${result.download_ms} ms` : "-"}
                      </TableCell>
                      <TableCell className="font-medium">
                        {result.total_ms ? `${result.total_ms} ms` : "-"}
                      </TableCell>
                      <TableCell>
                        {result.size_bytes ? `${(result.size_bytes / 1024).toFixed(2)} KB` : "-"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}