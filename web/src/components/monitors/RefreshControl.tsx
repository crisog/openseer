
import React from 'react';
import { RefreshCw, Pause } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

export interface RefreshInterval {
  value: number | null;
  label: string;
}

const refreshIntervals: RefreshInterval[] = [
  { value: null, label: 'Off' },
  { value: 120000, label: '2m' },
  { value: 300000, label: '5m' },
  { value: 600000, label: '10m' },
  { value: 900000, label: '15m' }
];

interface RefreshControlProps {
  onRefresh: () => void;
  refreshInterval: number | null;
  onRefreshIntervalChange: (interval: number | null) => void;
  isRefreshing?: boolean;
}

export function RefreshControl({
  onRefresh,
  refreshInterval,
  onRefreshIntervalChange,
  isRefreshing = false
}: RefreshControlProps): React.JSX.Element {
  const currentInterval = refreshIntervals.find((interval) => interval.value === refreshInterval);

  return (
    <div className="flex items-center gap-3">
      <Button
        variant="outline"
        size="sm"
        onClick={onRefresh}
        disabled={isRefreshing}
        className="flex items-center gap-2 hover:text-foreground"
      >
        <RefreshCw className="w-4 h-4" />
        Refresh
      </Button>

      <div className="flex items-center gap-2">
        <Select
          value={refreshInterval?.toString() || 'null'}
          onValueChange={(value) => {
            const interval = value === 'null' ? null : parseInt(value);
            const wasOff = refreshInterval === null;
            const isNowOn = interval !== null;

            onRefreshIntervalChange(interval);

            if (wasOff && isNowOn) {
              onRefresh();
            }
          }}
        >
          <SelectTrigger className="w-24">
            <SelectValue>
              <div className="flex items-center gap-1">
                {refreshInterval === null ? <Pause className="w-3 h-3" /> : <RefreshCw className="w-3 h-3" />}
                {currentInterval?.label || 'Off'}
              </div>
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {refreshIntervals.map((interval) => (
              <SelectItem key={interval.value?.toString() || 'null'} value={interval.value?.toString() || 'null'}>
                <div className="flex items-center gap-2">
                  {interval.value === null ? <Pause className="w-3 h-3" /> : <RefreshCw className="w-3 h-3" />}
                  {interval.label}
                </div>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  );
}