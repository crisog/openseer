'use client';

import React from 'react';
import { memo } from 'react'
import { useRouter } from 'next/navigation';
import { Pause, Play, Trash2, TrendingUp, BarChart3, Settings, Activity, Clock } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';

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
  uptimePercentage?: number;
}

function MonitorCardInner({
  monitor,
  onTogglePause,
  onDelete,
  onConfigure,
  isToggling = false,
  isDeleting = false,
  uptimePercentage
}: MonitorCardProps): React.JSX.Element {
  const router = useRouter();
  const intervalSeconds = Math.round(monitor.intervalMs / 1000);
  
  const handleViewDetails = () => {
    router.push(`/dashboard?id=${monitor.id}`);
  };

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
                {uptimePercentage !== undefined ? (
                  <span className="text-xs font-semibold">{uptimePercentage.toFixed(1)}%</span>
                ) : !monitor.enabled ? (
                  <span className="text-xs font-semibold text-muted-foreground">0.0%</span>
                ) : (
                  <span className="text-xs text-muted-foreground">Loading...</span>
                )}
              </div>
            </div>

            <div className="mt-2">
              <div className="flex items-center gap-1 mb-1">
                <Clock className="w-3 h-3 text-muted-foreground" />
                <p className="text-xs text-muted-foreground">Interval</p>
              </div>
              <div className="text-xs font-semibold">{intervalSeconds}s</div>
            </div>
          </div>

          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-1 mb-1">
              <TrendingUp className="w-3 h-3 text-muted-foreground" />
              <p className="text-xs text-muted-foreground">Response Time (24h)</p>
            </div>
            {/* Placeholder for MiniLatencyChart - to be implemented */}
            <div className="h-12 bg-secondary/20 rounded flex items-center justify-center">
              <span className="text-xs text-muted-foreground">Chart</span>
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
