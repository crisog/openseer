import { createFileRoute, useRouter } from '@tanstack/react-router'
import React, { useMemo, useState } from "react"
import { useSession } from "@/lib/auth-client"
import { ArrowLeft, Clock, Globe, Activity, AlertCircle, Loader2, RefreshCw, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Settings } from "lucide-react"
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
  PaginationState,
} from '@tanstack/react-table'

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
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useQuery } from "@connectrpc/connect-query"
import { MonitorsService } from "@/lib/gen/openseer/v1/monitors_pb"
import type { Monitor, MonitorResult as PbMonitorResult, MonitorMetrics as PbMonitorMetrics } from "@/lib/gen/openseer/v1/monitors_pb"
import { LatencyChart } from "@/components/monitors/LatencyChart"
import { UptimeChart } from "@/components/monitors/UptimeChart"
import { EditMonitorForm } from "@/components/monitors/EditMonitorForm"

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

function StatsCards({ results }: { results: MonitorResult[] }): React.JSX.Element {
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

  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-4">
      <Card className="surface !gap-1">
        <CardHeader className="pb-0 py-2">
          <CardTitle className="text-xs md:text-sm font-medium flex items-center gap-2">
            <Activity className="w-3 h-3" />
            Uptime
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-1 pb-2">
          <div className="text-sm md:text-base font-bold">{calculateUptime()}%</div>
          <p className="text-xs text-muted-foreground">Last {results.length} checks</p>
        </CardContent>
      </Card>

      <Card className="surface !gap-1">
        <CardHeader className="pb-0 py-2">
          <CardTitle className="text-xs md:text-sm font-medium flex items-center gap-2">
            <Clock className="w-3 h-3" />
            Avg Response
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-1 pb-2">
          <div className="text-sm md:text-base font-bold">{calculateAvgResponseTime()}ms</div>
        </CardContent>
      </Card>

      <Card className="surface !gap-1">
        <CardHeader className="pb-0 py-2">
          <CardTitle className="text-xs md:text-sm font-medium flex items-center gap-2">
            <Globe className="w-3 h-3" />
            Total Checks
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-1 pb-2">
          <div className="text-sm md:text-base font-bold">{results.length}</div>
        </CardContent>
      </Card>

      <Card className="surface !gap-1">
        <CardHeader className="pb-0 py-2">
          <CardTitle className="text-xs md:text-sm font-medium flex items-center gap-2">
            <AlertCircle className="w-3 h-3" />
            Failed Checks
          </CardTitle>
        </CardHeader>
        <CardContent className="pt-1 pb-2">
          <div className="text-sm md:text-base font-bold">{results.filter(r => r.status !== "OK").length}</div>
        </CardContent>
      </Card>
    </div>
  )
}

function ResultsTable({ results }: { results: MonitorResult[] }): React.JSX.Element {
  const columnHelper = createColumnHelper<MonitorResult>()
  const columns = React.useMemo(
    () => [
      columnHelper.accessor('status', {
        header: 'Status',
        cell: info => (
          <Badge variant={getStatusBadgeVariant(info.getValue())}>
            {info.getValue()}
          </Badge>
        ),
      }),
      columnHelper.accessor('event_at', {
        header: 'Timestamp',
        cell: info => new Date(info.getValue()).toLocaleString(),
      }),
      columnHelper.accessor('region', {
        header: 'Region',
        cell: info => info.getValue(),
      }),
      columnHelper.accessor('http_code', {
        header: 'HTTP Code',
        cell: info => info.getValue() || '-',
      }),
      columnHelper.accessor('total_ms', {
        header: 'Response Time',
        cell: info => info.getValue() ? `${info.getValue()} ms` : '-',
      }),
    ],
    []
  )

  const [pagination, setPagination] = React.useState<PaginationState>({ pageIndex: 0, pageSize: 10 })
  const table = useReactTable({ data: results, columns, state: { pagination }, onPaginationChange: setPagination, getCoreRowModel: getCoreRowModel(), getPaginationRowModel: getPaginationRowModel() })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent Monitor Results</CardTitle>
        <CardDescription>Showing {table.getRowModel().rows.length} of {results.length} results</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="w-full overflow-x-auto">
          <Table>
            <TableHeader>
              {table.getHeaderGroups().map(headerGroup => (
                <TableRow key={headerGroup.id}>
                  {headerGroup.headers.map(header => (
                    <TableHead key={header.id}>
                      {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                    </TableHead>
                  ))}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody>
              {table.getRowModel().rows.length ? (
                table.getRowModel().rows.map(row => (
                  <TableRow key={row.id}>
                    {row.getVisibleCells().map(cell => (
                      <TableCell key={cell.id}>
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : (
                <TableRow>
                  <TableCell colSpan={columns.length} className="h-24 text-center">No results</TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        <div className="flex items-center justify-between">
          <div className="text-sm text-muted-foreground">Page {table.getState().pagination.pageIndex + 1} of {table.getPageCount()}</div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => table.firstPage()} disabled={!table.getCanPreviousPage()}>
              <ChevronsLeft className="h-4 w-4" />
            </Button>
            <Button variant="outline" size="sm" onClick={() => table.previousPage()} disabled={!table.getCanPreviousPage()}>
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <Button variant="outline" size="sm" onClick={() => table.nextPage()} disabled={!table.getCanNextPage()}>
              <ChevronRight className="h-4 w-4" />
            </Button>
            <Button variant="outline" size="sm" onClick={() => table.lastPage()} disabled={!table.getCanNextPage()}>
              <ChevronsRight className="h-4 w-4" />
            </Button>
            <select
              value={table.getState().pagination.pageSize}
              onChange={e => { table.setPageSize(Number(e.target.value)) }}
              className="h-8 w-[70px] rounded-md border border-input bg-background px-2 text-sm"
            >
              {[10, 20, 30, 50].map(pageSize => (
                <option key={pageSize} value={pageSize}>{pageSize}</option>
              ))}
            </select>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

export const Route = createFileRoute('/_dashboard/monitors/$id')({
  component: MonitorDetailsPage,
})

function MonitorDetailsPage() {
  const router = useRouter()
  const { id: monitorId } = Route.useParams()
  const session = useSession()

  const [refreshing, setRefreshing] = useState(false)
  const [isConfigureModalOpen, setIsConfigureModalOpen] = useState(false)
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

  const handleBackClick = () => {
    router.navigate({ to: "/monitors" })
  }

  const handleConfigureSuccess = () => {
    setIsConfigureModalOpen(false)
    detailsQuery.refetch()
    resultsQuery.refetch()
    metricsQuery.refetch()
  }

  const handleConfigureCancel = () => {
    setIsConfigureModalOpen(false)
  }

  const loading = detailsQuery.isPending || resultsQuery.isPending || metricsQuery.isPending

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-[60vh]">
        <Loader2 className="h-8 w-8 animate-spin" />
      </div>
    )
  }

  if (!monitor) {
    return (
      <div className="text-center py-12">
        <h2 className="text-2xl font-bold mb-4">Monitor not found</h2>
        <Button onClick={handleBackClick}>
          <ArrowLeft className="h-4 w-4 mr-2" />
          Back to Monitors
        </Button>
      </div>
    )
  }

  return (
    <div className="p-4 sm:p-6 lg:p-8 space-y-4 pt-4 sm:pt-6 overflow-x-hidden">
      {/* Header */}
      <div className="flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
        <div className="flex items-center gap-2 md:gap-3">
          <Button variant="ghost" size="sm" onClick={handleBackClick}>
            <ArrowLeft className="w-4 h-4" />
          </Button>
          <div className="min-w-0 flex-1">
            <h1 className="text-lg md:text-xl font-semibold truncate">{monitor.name || monitor.url}</h1>
            <p className="text-muted-foreground font-mono text-xs truncate">{monitor.url}</p>
          </div>
        </div>
        <div className="flex items-center gap-2 md:flex-shrink-0">
          <Badge variant={monitor.enabled ? "default" : "secondary"} className="text-xs">
            {monitor.enabled ? "Active" : "Paused"}
          </Badge>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setIsConfigureModalOpen(true)}
            className="h-7 px-2"
          >
            <Settings className="w-3 h-3 mr-1" />
            Configure
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={refreshData}
            disabled={refreshing}
            className="h-7 px-2"
          >
            {refreshing ? (
              <Loader2 className="w-3 h-3 animate-spin" />
            ) : (
              <RefreshCw className="w-3 h-3" />
            )}
            <span className="ml-1">Refresh</span>
          </Button>
        </div>
      </div>

      <StatsCards results={results} />

      {/* Charts */}
      <div className="grid grid-cols-1 gap-6">
        <UptimeChart monitorId={monitorId} />
        <LatencyChart monitorId={monitorId} />
      </div>

      <ResultsTable results={results} />

      {/* Configure Modal */}
      <Dialog open={isConfigureModalOpen} onOpenChange={setIsConfigureModalOpen}>
        <DialogContent className="surface max-w-2xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Configure Monitor</DialogTitle>
          </DialogHeader>
          {detailsQuery.data?.monitor && (
            <EditMonitorForm
              monitor={detailsQuery.data.monitor}
              onSuccess={handleConfigureSuccess}
              onCancel={handleConfigureCancel}
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
