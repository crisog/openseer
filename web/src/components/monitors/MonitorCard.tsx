
import React from 'react';
import { memo } from 'react'
import { useRouter } from '@tanstack/react-router';
import { Pause, Play, Trash2, TrendingUp, BarChart3, Settings, Activity } from 'lucide-react';
import { useQuery } from '@connectrpc/connect-query';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';

interface MonitorCardProps {
  monitor: {
    id: string;
    name: string;
    url: string;
    method: string;
    expectedStatus: number;
    intervalMs: number;
    enabled: boolean;
  };
  onTogglePause: (id: string) => void;
  onDelete: (id: string) => void;
  onConfigure: (id: string) => void;
  isToggling?: boolean;
  isDeleting?: boolean;
  lastRefresh?: Date | null;
}

function MonitorCardInner({
  monitor,
  onTogglePause,
  onDelete,
  onConfigure,
  isToggling = false,
  isDeleting = false,
  lastRefresh
}: MonitorCardProps): React.JSX.Element {
  const router = useRouter();
  const intervalSeconds = Math.round(monitor.intervalMs / 1000);

  const uptimeQuery = useQuery(
    MonitorsService.method.getMonitorUptime,
    { monitorId: monitor.id, timeRange: '24h' },
    {
      enabled: Boolean(monitor.id),
      staleTime: 2 * 60 * 1000,
    }
  );

  const metricsQuery = useQuery(
    MonitorsService.method.getMonitorMetrics,
    { monitorId: monitor.id },
    {
      enabled: Boolean(monitor.id),
      staleTime: 2 * 60 * 1000,
    }
  );

  const uptimePercentage = React.useMemo(() => {
    const value = uptimeQuery.data?.uptimePercentage;
    if (typeof value !== 'number' || Number.isNaN(value)) {
      return null;
    }
    return value;
  }, [uptimeQuery.data]);

  const averageResponseMs = React.useMemo(() => {
    const metrics = metricsQuery.data?.metrics;
    if (!metrics || metrics.length === 0) return null;

    let weightedSum = 0;
    let weightedCount = 0;

    metrics.forEach(metric => {
      const avg = metric.avgMs;
      if (typeof avg !== 'number' || !Number.isFinite(avg)) {
        return;
      }

      const countValue = Number(metric.count);
      if (Number.isFinite(countValue) && countValue > 0) {
        weightedSum += avg * countValue;
        weightedCount += countValue;
      }
    });

    if (weightedCount > 0) {
      return weightedSum / weightedCount;
    }

    const fallbacks = metrics
      .map(metric => metric.avgMs)
      .filter(ms => typeof ms === 'number' && Number.isFinite(ms));

    if (!fallbacks.length) {
      return null;
    }

    const total = fallbacks.reduce((acc, ms) => acc + ms, 0);
    return total / fallbacks.length;
  }, [metricsQuery.data]);

  const { refetch: refetchUptime } = uptimeQuery;
  const { refetch: refetchMetrics } = metricsQuery;

  React.useEffect(() => {
    if (!lastRefresh) return;
    void refetchUptime();
    void refetchMetrics();
  }, [lastRefresh, refetchUptime, refetchMetrics]);

  const handleViewDetails = () => {
    router.navigate({ to: `/monitors/${monitor.id}` });
  };

  const uptimeDisplay = React.useMemo(() => {
    if (uptimeQuery.isPending) {
      return 'Loading...';
    }
    if (uptimeQuery.isError) {
      return '—';
    }
    if (uptimePercentage === null) {
      return '—';
    }
    return `${uptimePercentage.toFixed(2)}%`;
  }, [uptimeQuery.isPending, uptimeQuery.isError, uptimePercentage]);

  const averageResponseDisplay = React.useMemo(() => {
    if (metricsQuery.isPending) {
      return 'Loading...';
    }
    if (metricsQuery.isError) {
      return '—';
    }
    if (averageResponseMs === null) {
      return 'No data';
    }
    return `${Math.round(averageResponseMs)} ms`;
  }, [metricsQuery.isPending, metricsQuery.isError, averageResponseMs]);

  return (
    <Card className="surface p-2 gap-2">
      <CardHeader className="pb-2 pt-2 px-2">
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="text-xs font-medium leading-tight">{monitor.name}</CardTitle>
          <Badge variant={!monitor.enabled ? 'secondary' : 'default'} className="shrink-0 text-xs px-1.5 py-0">
            {!monitor.enabled ? 'Paused' : 'Active'}
          </Badge>
        </div>
        <p className="text-xs font-mono text-muted-foreground break-all">{monitor.url}</p>
        <div className="flex items-center gap-1 flex-wrap mt-1">
          <Badge variant="outline" className="text-xs px-1.5 py-0">
            {monitor.method}
          </Badge>
          <Badge variant="outline" className="text-xs px-1.5 py-0">
            {monitor.expectedStatus}
          </Badge>
          <Badge variant="outline" className="text-xs px-1.5 py-0">
            {intervalSeconds}s
          </Badge>
        </div>
      </CardHeader>

      <CardContent className="space-y-2 px-2 pb-2">
        <div className="flex gap-3">
          <div className="flex-shrink-0 w-20">
            <div className="flex items-center gap-1 mb-1">
              <Activity className="w-3 h-3 text-muted-foreground" />
              <p className="text-xs text-muted-foreground">Uptime</p>
            </div>
            <div className="space-y-1">
              <div className="flex items-baseline gap-1">
                <span className="text-sm font-semibold">{uptimeDisplay}</span>
              </div>
            </div>
          </div>

          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-1 mb-1">
              <TrendingUp className="w-3 h-3 text-muted-foreground" />
              <p className="text-xs text-muted-foreground">Response Time (24h)</p>
            </div>
            <div className="flex items-baseline gap-2">
              <span className="text-sm font-semibold leading-tight text-foreground">{averageResponseDisplay}</span>
            </div>
          </div>
        </div>

        <div className="flex gap-1.5 mt-4">
          <Button
            size="sm"
            variant="outline"
            onClick={handleViewDetails}
            className="flex-1 min-w-0 hover:text-foreground h-7 text-xs px-2"
          >
            <BarChart3 className="w-3 h-3 mr-1 shrink-0" />
            <span className="truncate">Details</span>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => onConfigure(monitor.id)}
            className="shrink-0 hover:text-foreground h-7 w-7 p-0"
          >
            <Settings className="w-3 h-3" />
          </Button>
          <Button
            size="sm"
            variant="secondary"
            onClick={() => onTogglePause(monitor.id)}
            disabled={isToggling}
            className="shrink-0 h-7 w-7 p-0"
          >
            {!monitor.enabled ? <Play className="w-3 h-3" /> : <Pause className="w-3 h-3" />}
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={() => onDelete(monitor.id)}
            disabled={isDeleting}
            className="shrink-0 h-7 w-7 p-0"
          >
            <Trash2 className="w-3 h-3" />
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export const MonitorCard = memo(MonitorCardInner);
