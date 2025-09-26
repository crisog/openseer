
import * as React from 'react';
import { Activity } from 'lucide-react';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useQuery } from '@connectrpc/connect-query';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';

import { UptimeBar } from './UptimeBar';

type UptimeTimeRange = '24h' | '7d' | '30d';

interface UptimeChartProps {
  monitorId: string;
}

export function UptimeChart({ monitorId }: UptimeChartProps): React.JSX.Element {
  const [timeRange, setTimeRange] = React.useState<UptimeTimeRange>('24h');
  
  const uptimeQuery = useQuery(
    MonitorsService.method.getMonitorUptime,
    { monitorId, timeRange },
    { enabled: !!monitorId, retry: false }
  );

  const uptimeData = React.useMemo(() => {
    if (!uptimeQuery.data) return null;
    return {
      totalChecks: uptimeQuery.data.totalChecks,
      successfulChecks: uptimeQuery.data.successfulChecks,
      failedChecks: uptimeQuery.data.failedChecks,
      uptimePercentage: uptimeQuery.data.uptimePercentage,
    };
  }, [uptimeQuery.data]);

  const getTimeRangeLabel = (range: UptimeTimeRange): string => {
    switch (range) {
      case '24h':
        return 'Last 24 Hours';
      case '7d':
        return 'Last 7 Days';
      case '30d':
        return 'Last 30 Days';
    }
  };

  if (uptimeQuery.isPending && !uptimeQuery.data) {
    return (
      <Card className="surface gap-3">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-6">
          <div className="space-y-1">
            <CardTitle className="text-xl font-semibold">Uptime</CardTitle>
            <CardDescription className="text-sm text-muted-foreground">{getTimeRangeLabel(timeRange)}</CardDescription>
          </div>
          <Select value={timeRange} onValueChange={(value: UptimeTimeRange) => setTimeRange(value)}>
            <SelectTrigger className="w-[140px] hover:text-foreground">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="24h">24 Hours</SelectItem>
              <SelectItem value="7d">7 Days</SelectItem>
              <SelectItem value="30d">30 Days</SelectItem>
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent className="pb-0">
          <div className="h-[300px] flex items-center justify-center">
            <div className="text-center space-y-3">
              <Activity className="w-8 h-8 mx-auto text-muted-foreground animate-pulse" />
              <p className="text-sm text-muted-foreground">Loading uptime metrics...</p>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (uptimeQuery.isError) {
    return (
      <Card className="surface gap-3">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-6">
          <div className="space-y-1">
            <CardTitle className="text-xl font-semibold">Uptime</CardTitle>
            <CardDescription className="text-sm text-muted-foreground">{getTimeRangeLabel(timeRange)}</CardDescription>
          </div>
          <Select value={timeRange} onValueChange={(value: UptimeTimeRange) => setTimeRange(value)}>
            <SelectTrigger className="w-[140px] hover:text-foreground">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="24h">24 Hours</SelectItem>
              <SelectItem value="7d">7 Days</SelectItem>
              <SelectItem value="30d">30 Days</SelectItem>
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent className="pb-0">
          <div className="h-[300px] flex items-center justify-center">
            <div className="text-center space-y-3">
              <Activity className="w-8 h-8 mx-auto text-muted-foreground" />
              <p className="text-sm text-muted-foreground">No data available yet</p>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="surface gap-3 transition-all duration-300">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-6">
        <div className="space-y-1">
          <CardTitle className="text-xl font-semibold">Uptime</CardTitle>
          <CardDescription className="text-sm text-muted-foreground">{getTimeRangeLabel(timeRange)}</CardDescription>
        </div>
        <Select value={timeRange} onValueChange={(value: UptimeTimeRange) => setTimeRange(value)}>
          <SelectTrigger className="w-[140px] hover:text-foreground">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="24h">24 Hours</SelectItem>
            <SelectItem value="7d">7 Days</SelectItem>
            <SelectItem value="30d">30 Days</SelectItem>
          </SelectContent>
        </Select>
      </CardHeader>
      <CardContent className="pb-0">
        <div className="flex flex-col justify-center">
          <div className="text-center space-y-4">
            <div className="space-y-2 py-2">
              <UptimeBar monitorId={monitorId} timeRange={timeRange} uptimeData={uptimeData} />
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}