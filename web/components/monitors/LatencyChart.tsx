'use client';

import * as React from 'react';
import { Line, LineChart, CartesianGrid, XAxis, YAxis, ReferenceArea, ReferenceLine } from 'recharts';
import { type ChartConfig, ChartContainer, ChartTooltip } from '@/components/ui/chart';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useQuery } from '@connectrpc/connect-query';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';

type MonitorLatencyTimeRange = '1h' | '7d' | '30d' | '90d';

interface LatencyChartProps {
  monitorId: string;
  monitorName?: string;
}

export function LatencyChart({ monitorId }: LatencyChartProps): React.JSX.Element {
  const [timeRange, setTimeRange] = React.useState<MonitorLatencyTimeRange>('1h');
  const rangeToLimit: Record<MonitorLatencyTimeRange, number> = React.useMemo(() => ({
    '1h': 200,
    '7d': 1000,
    '30d': 2000,
    '90d': 4000,
  }), []);

  const resultsQuery = useQuery(
    MonitorsService.method.getMonitorResults,
    { monitorId, limit: rangeToLimit[timeRange] },
    { enabled: !!monitorId }
  );

  const chartData = React.useMemo((): { date: string; timestamp: number; latency: number | null }[] => {
    const results = resultsQuery.data?.results ?? [];
    return results
      .map((r) => {
        const ts = r.eventAt ? new Date(Number(r.eventAt.seconds) * 1000) : null;
        if (!ts) return null;
        return {
          date: ts.toISOString(),
          timestamp: ts.getTime(),
          latency: r.status === 'OK' ? (r.totalMs ?? null) : null,
        };
      })
      .filter((p): p is NonNullable<typeof p> => p !== null)
      .sort((a, b) => a.timestamp - b.timestamp);
  }, [resultsQuery.data]);

  const incidents = React.useMemo(() => {
    const results = resultsQuery.data?.results ?? [];
    if (!results.length) return [] as { startTime: Date; endTime: Date }[];

    const sorted = [...results].sort((a, b) => {
      const ta = a.eventAt ? Number(a.eventAt.seconds) : 0;
      const tb = b.eventAt ? Number(b.eventAt.seconds) : 0;
      return ta - tb;
    });

    const incidentList: { startTime: Date; endTime: Date }[] = [];
    let downtimeStart: Date | null = null;

    sorted.forEach((r) => {
      const t = r.eventAt ? new Date(Number(r.eventAt.seconds) * 1000) : null;
      if (!t) return;
      if (r.status !== 'OK' && downtimeStart === null) {
        downtimeStart = t;
      } else if (r.status === 'OK' && downtimeStart !== null) {
        incidentList.push({ startTime: downtimeStart, endTime: t });
        downtimeStart = null;
      }
    });

    if (downtimeStart !== null && sorted.length > 0) {
      const last = sorted[sorted.length - 1];
      const end = last.eventAt ? new Date(Number(last.eventAt.seconds) * 1000) : new Date();
      incidentList.push({ startTime: downtimeStart, endTime: end });
    }

    return incidentList;
  }, [resultsQuery.data]);

  const chartConfig: ChartConfig = {
    latency: {
      label: 'Response Time (ms)',
      color: 'var(--chart-1)'
    }
  };

  if (resultsQuery.isPending) {
    return (
      <Card className="surface">
        <CardHeader className="pb-6">
          <CardTitle className="text-xl font-semibold">Response Time</CardTitle>
          <CardDescription className="text-sm text-muted-foreground">Loading latency data...</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="h-[280px] md:h-[320px] flex items-center justify-center">
            <div className="text-center space-y-3">
              <div className="w-8 h-8 mx-auto border-2 border-muted-foreground border-t-foreground rounded-full animate-spin" />
              <p className="text-sm text-muted-foreground">Loading response time data...</p>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (resultsQuery.isError) {
    return (
      <Card className="surface">
        <CardHeader className="pb-6">
          <CardTitle className="text-xl font-semibold">Response Time</CardTitle>
          <CardDescription className="text-sm text-muted-foreground">Failed to load latency data</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="h-[280px] md:h-[320px] flex items-center justify-center">
            <div className="text-center space-y-3">
              <div className="w-8 h-8 mx-auto text-red-500">⚠️</div>
              <div className="text-red-500 text-sm">Error loading data</div>
              <div className="text-xs text-muted-foreground">{resultsQuery.error?.message || 'Unknown error occurred'}</div>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (!chartData.length) {
    return (
      <Card className="surface">
        <CardHeader className="pb-6">
          <CardTitle className="text-xl font-semibold">Response Time</CardTitle>
          <CardDescription className="text-sm text-muted-foreground">No latency data available</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="h-[280px] md:h-[320px] flex items-center justify-center">
            <div className="text-center space-y-3">
              <div className="w-8 h-8 mx-auto text-muted-foreground">📊</div>
              <p className="text-sm text-muted-foreground">No data to display</p>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  const handleTimeRangeChange = (value: string): void => {
    setTimeRange(value as MonitorLatencyTimeRange);
  };

  return (
    <Card className="surface transition-all duration-300">
      <CardHeader className="flex items-center gap-2 space-y-0 border-b pb-6 sm:flex-row">
        <div className="grid flex-1 gap-1">
          <CardTitle className="text-xl font-semibold">Response Time</CardTitle>
          <CardDescription className="text-sm text-muted-foreground">Monitor response times over the selected period</CardDescription>
        </div>
        <Select value={timeRange} onValueChange={handleTimeRangeChange}>
          <SelectTrigger className="hidden w-[160px] rounded-lg sm:ml-auto sm:flex hover:text-foreground" aria-label="Select time range">
            <SelectValue placeholder="Last hour" />
          </SelectTrigger>
          <SelectContent className="rounded-xl">
            <SelectItem value="1h" className="rounded-lg">
              Last hour
            </SelectItem>
            <SelectItem value="7d" className="rounded-lg">
              Last 7 days
            </SelectItem>
            <SelectItem value="30d" className="rounded-lg">
              Last 30 days
            </SelectItem>
            <SelectItem value="90d" className="rounded-lg">
              Last 90 days
            </SelectItem>
          </SelectContent>
        </Select>
      </CardHeader>
      <CardContent className="px-2 pt-4 sm:px-6 sm:pt-6">
        <ChartContainer config={chartConfig} className="aspect-auto h-[280px] md:h-[320px] w-full">
          <LineChart data={chartData}>
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="timestamp"
              type="number"
              domain={['dataMin', 'dataMax']}
              tickFormatter={(value) => {
                const date = new Date(value);
                return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
              }}
              tickLine={false}
              axisLine={false}
              tick={false}
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              tickFormatter={(value): string => `${value}ms`}
              className="text-xs text-muted-foreground"
            />
            <ChartTooltip
              cursor={false}
              content={({ active, payload }) => {
                if (!active || !payload?.length) return null;

                const dataPoint = payload[0]?.payload;
                const timestamp = dataPoint?.timestamp;

                let formattedLabel = 'Unknown time';
                if (timestamp && typeof timestamp === 'number') {
                  const date = new Date(timestamp);
                  if (!isNaN(date.getTime())) {
                    const formatOptions: Intl.DateTimeFormatOptions = {
                      month: 'short',
                      day: 'numeric',
                      hour: 'numeric',
                      minute: '2-digit'
                    };

                    if (timeRange === '1h') {
                      formatOptions.second = '2-digit';
                    }

                    formattedLabel = date.toLocaleDateString('en-US', formatOptions);
                  }
                }

                const latencyValue = payload[0]?.value;
                const formattedValue = latencyValue === null ? 'Service Unavailable' : `${latencyValue}ms`;

                return (
                  <div className="rounded-lg border bg-background p-2 shadow-md">
                    <div className="grid gap-2">
                      <div className="flex flex-col">
                        <span className="text-[0.70rem] uppercase text-muted-foreground">{formattedLabel}</span>
                        <span className="font-bold text-foreground">{formattedValue}</span>
                      </div>
                    </div>
                  </div>
                );
              }}
            />

            <Line dataKey="latency" type="monotone" stroke="var(--chart-1)" strokeWidth={2} dot={false} connectNulls={false} />

            {/* Shaded downtime areas */}
            {incidents.map((incident, index) => (
              <ReferenceArea
                key={`downtime-area-${index}`}
                x1={incident.startTime.getTime()}
                x2={incident.endTime.getTime()}
                stroke="none"
                fill="#EF4444"
                fillOpacity={0.15}
                ifOverflow="visible"
              />
            ))}

            {/* Downtime boundary lines */}
            {incidents.map((incident, index) => (
              <React.Fragment key={`downtime-lines-${index}`}>
                <ReferenceLine x={incident.startTime.getTime()} stroke="#EF4444" strokeWidth={2} strokeDasharray="4 4" />
                <ReferenceLine x={incident.endTime.getTime()} stroke="#EF4444" strokeWidth={2} strokeDasharray="4 4" />
              </React.Fragment>
            ))}
          </LineChart>
        </ChartContainer>
      </CardContent>
    </Card>
  );
}