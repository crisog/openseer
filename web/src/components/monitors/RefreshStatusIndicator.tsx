
import React, { memo } from 'react';
import { useEffect, useState } from 'react';

interface RefreshStatusIndicatorProps {
  refreshInterval: number | null;
  lastRefresh: Date | null;
  isRefreshing: boolean;
}

function RefreshStatusIndicatorInner({ refreshInterval, lastRefresh }: RefreshStatusIndicatorProps): React.JSX.Element | null {
  const [currentTime, setCurrentTime] = useState(new Date());

  useEffect(() => {
    const timer = setInterval(() => {
      setCurrentTime(new Date());
    }, 1000);

    return (): void => clearInterval(timer);
  }, []);

  const formatLastRefresh = (date: Date): string => {
    const diffMs = currentTime.getTime() - date.getTime();
    const diffSeconds = Math.max(0, Math.floor(diffMs / 1000));

    if (diffSeconds < 60) {
      return `${diffSeconds}s ago`;
    } else {
      const diffMinutes = Math.floor(diffSeconds / 60);
      return `${diffMinutes}m ago`;
    }
  };

  if (!lastRefresh) return null;

  return (
    <div className="flex items-center gap-1">
      {refreshInterval !== null && <div className="w-1.5 h-1.5 bg-green-500 rounded-full"></div>}
      <span className="text-xs text-muted-foreground">Updated {formatLastRefresh(lastRefresh)}</span>
    </div>
  );
}

export const RefreshStatusIndicator = memo(RefreshStatusIndicatorInner);