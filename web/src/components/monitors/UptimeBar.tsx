
import React, { useState, useEffect, useRef, memo } from 'react';
import { useQuery } from '@connectrpc/connect-query';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';

type UptimeTimeRange = '24h' | '7d' | '30d';

interface UptimeBarProps {
  monitorId: string;
  timeRange: UptimeTimeRange;
  className?: string;
  uptimeData?: {
    totalChecks: bigint;
    successfulChecks: bigint;
    failedChecks: bigint;
    uptimePercentage: number;
  } | null;
}

interface UptimeIncident {
  time: number;
  duration: number;
  startTime?: Date;
  endTime?: Date;
}

interface IncidentPosition {
  start: number;
  width: number;
  incident: UptimeIncident;
  index: number;
}

interface AvailabilityPoint {
  timestamp: string;
  available: 0 | 1;
}

function UptimeBarInner({ monitorId, timeRange, className, uptimeData }: UptimeBarProps): React.JSX.Element {
  const [isClient, setIsClient] = useState(false);
  
  const timelineQuery = useQuery(
    MonitorsService.method.getMonitorUptimeTimeline,
    { monitorId, timeRange },
    { enabled: !!monitorId, retry: false }
  );

  const [tooltip, setTooltip] = useState<{
    show: boolean;
    isIncident: boolean;
    content: string;
    subContent?: string;
    position: number;
  }>({
    show: false,
    isIncident: false,
    content: '',
    position: 0
  });
  const barRef = useRef<HTMLDivElement>(null);

  const { availabilityData, timelineData } = React.useMemo(() => {
    const points: AvailabilityPoint[] = [];
    const timeline: Array<{
      bucket: Date;
      totalChecks: bigint;
      successfulChecks: bigint;
      uptimePercentage: number;
    }> = [];

    if (timelineQuery.data?.data) {
      timelineQuery.data.data.forEach(point => {
        const bucketTime = point.bucket ? new Date(Number(point.bucket.seconds) * 1000 + Number(point.bucket.nanos) / 1000000) : new Date();
        
        timeline.push({
          bucket: bucketTime,
          totalChecks: point.totalChecks,
          successfulChecks: point.successfulChecks,
          uptimePercentage: point.uptimePercentage,
        });
        
        const isAvailable = point.uptimePercentage >= 99.5 ? 1 : 0;
        points.push({
          timestamp: bucketTime.toISOString(),
          available: isAvailable as 0 | 1,
        });
      });
    }

    return { availabilityData: points, timelineData: timeline };
  }, [timelineQuery.data]);

  useEffect(() => {
    setIsClient(true);
  }, []);

  const { incidents, overallUptime, timelineStart, timelineEnd, totalChecks } = React.useMemo(() => {
    if (!availabilityData?.length) {
      return { incidents: [], overallUptime: 100, timelineStart: new Date(), timelineEnd: new Date(), totalChecks: 0 };
    }

    const sequence = availabilityData;
    const totalChecks = sequence.length;
    const successfulChecks = sequence.filter((point) => point.available === 1).length;
    const uptime = totalChecks > 0 ? (successfulChecks / totalChecks) * 100 : 100;

    const startTime = new Date(sequence[0].timestamp);
    const endTime = new Date(sequence[sequence.length - 1].timestamp);
    const totalDuration = endTime.getTime() - startTime.getTime();

    const incidentList: UptimeIncident[] = [];
    let downtimeStart: number | null = null;
    let downtimeStartTime: Date | null = null;

    sequence.forEach((point) => {
      const pointTime = new Date(point.timestamp).getTime();
      const position = ((pointTime - startTime.getTime()) / totalDuration) * 100;

      if (point.available === 0 && downtimeStart === null) {
        downtimeStart = position;
        downtimeStartTime = new Date(point.timestamp);
      } else if (point.available === 1 && downtimeStart !== null && downtimeStartTime) {
        incidentList.push({
          time: downtimeStart,
          duration: position - downtimeStart,
          startTime: downtimeStartTime,
          endTime: new Date(point.timestamp)
        });
        downtimeStart = null;
        downtimeStartTime = null;
      }
    });

    if (downtimeStart !== null && downtimeStartTime) {
      incidentList.push({
        time: downtimeStart,
        duration: 100 - downtimeStart,
        startTime: downtimeStartTime,
        endTime: endTime
      });
    }

    return {
      incidents: incidentList,
      overallUptime: uptime,
      timelineStart: startTime,
      timelineEnd: endTime,
      totalChecks
    };
  }, [availabilityData]);

  const incidentPositions: IncidentPosition[] = React.useMemo(() => {
    return incidents.map((incident, index) => {
      const startPosition = incident.time;
      const width = incident.duration;

      return {
        start: startPosition,
        width: width,
        incident,
        index
      };
    });
  }, [incidents]);

  const getIncidentAtPosition = (position: number): IncidentPosition | undefined => {
    return incidentPositions.find((item) => position >= item.start && position <= item.start + item.width);
  };

  const formatDuration = (seconds: number): string => {
    const minutes = Math.floor(seconds / 60);
    const remainingSeconds = seconds % 60;
    return `${minutes}min ${remainingSeconds}s`;
  };

  const handleBarHover = (e: React.MouseEvent<HTMLDivElement>): void => {
    if (!barRef.current) return;

    const rect = barRef.current.getBoundingClientRect();
    const position = ((e.clientX - rect.left) / rect.width) * 100;

    const incidentAtPosition = getIncidentAtPosition(position);

    if (incidentAtPosition && incidentAtPosition.incident.startTime && incidentAtPosition.incident.endTime) {
      const duration = Math.round((incidentAtPosition.incident.endTime.getTime() - incidentAtPosition.incident.startTime.getTime()) / 1000);

      const formatDate = (date: Date) => {
        return new Intl.DateTimeFormat('en-US', {
          weekday: 'short',
          day: 'numeric',
          month: 'short',
          year: 'numeric',
          hour: '2-digit',
          minute: '2-digit'
        }).format(date);
      };

      const startFormatted = formatDate(incidentAtPosition.incident.startTime);
      const endTime = incidentAtPosition.incident.endTime.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

      setTooltip({
        show: true,
        isIncident: true,
        content: `${startFormatted} → ${endTime}`,
        subContent: formatDuration(duration),
        position: incidentAtPosition.start + incidentAtPosition.width / 2
      });
    } else {
      const totalDuration = timelineEnd.getTime() - timelineStart.getTime();
      const hoveredTime = new Date(timelineStart.getTime() + (totalDuration * position) / 100);

      const formatDate = (date: Date) => {
        return new Intl.DateTimeFormat('en-US', {
          weekday: 'short',
          day: 'numeric',
          month: 'short',
          year: 'numeric',
          hour: '2-digit',
          minute: '2-digit'
        }).format(date);
      };

      setTooltip({
        show: true,
        isIncident: false,
        content: formatDate(hoveredTime),
        position: position
      });
    }
  };

  const handleMouseLeave = (): void => {
    setTooltip({ ...tooltip, show: false });
  };

  const getRangeLabel = (): string => {
    if (!availabilityData?.length) return `-${timeRange}`;
    let start = timelineStart.getTime();
    let end = timelineEnd.getTime();
    if (availabilityData.length === 1) {
      if (timeRange === '24h') end = start + 60 * 60 * 1000;
      else if (timeRange === '7d') end = start + 2 * 60 * 60 * 1000;
      else if (timeRange === '30d') end = start + 24 * 60 * 60 * 1000;
    }
    const diffMs = Math.max(0, end - start);
    const minutes = Math.floor(diffMs / 60000);
    if (minutes < 60) return `-${minutes}m`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `-${hours}h`;
    const days = Math.floor(hours / 24);
    return `-${days}d`;
  };

  if (!isClient) {
    return <div className="animate-pulse bg-secondary/20 rounded-lg h-40 w-full"></div>;
  }

  if (timelineQuery.isPending) {
    return (
      <div className={className || ''}>
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-lg font-medium text-muted-foreground">UPTIME</h2>
          <div className="text-3xl font-bold text-foreground">---%</div>
        </div>
        <div className="relative h-2 rounded-full overflow-hidden animate-pulse" style={{ backgroundColor: 'var(--chart-1)' }}></div>
        <div className="flex justify-between items-center mt-2 text-sm text-muted-foreground">
          <div>-{timeRange}</div>
          {totalChecks > 0 && <div className="text-xs">{totalChecks} checks</div>}
          <div>Now</div>
        </div>
      </div>
    );
  }

  if (!availabilityData?.length) {
    return (
      <div className={className || ''}>
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-lg font-medium text-muted-foreground">UPTIME</h2>
          <div className="text-3xl font-bold text-foreground">---%</div>
        </div>
        <div className="relative h-2 bg-secondary rounded-full overflow-hidden">
          <div className="absolute inset-0 flex items-center justify-center">
            <span className="text-xs text-muted-foreground">No data</span>
          </div>
        </div>
        <div className="flex justify-between items-center mt-2 text-sm text-muted-foreground">
          <div>-{timeRange}</div>
          {totalChecks > 0 && <div className="text-xs">{totalChecks} checks</div>}
          <div>Now</div>
        </div>
      </div>
    );
  }

  return (
    <div className={className || ''}>
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-lg font-medium text-muted-foreground">UPTIME</h2>
        <div className="text-3xl font-bold text-foreground">
          {uptimeData ? uptimeData.uptimePercentage.toFixed(3) : overallUptime.toFixed(3)}%
        </div>
      </div>

      <div className="relative mb-4" onMouseLeave={handleMouseLeave}>
        {tooltip.show && (
          <div
            className={`absolute bottom-full mb-2 transform -translate-x-1/2 z-20 ${
              tooltip.isIncident
                ? 'bg-background text-foreground border border-border'
                : 'bg-background text-foreground border border-border'
            } rounded-md px-3 py-2 text-sm whitespace-nowrap shadow-lg`}
            style={{ left: `${Math.max(10, Math.min(90, tooltip.position))}%` }}
          >
            <div className="font-medium">{tooltip.content}</div>
            {tooltip.subContent && <div className="text-muted-foreground text-xs mt-1">{tooltip.subContent}</div>}
            {!tooltip.isIncident && (
              <div className="absolute bottom-0 left-1/2 transform -translate-x-1/2 translate-y-full h-2 border-l border-dashed border-muted-foreground"></div>
            )}
          </div>
        )}

        <div
          ref={barRef}
          className="relative h-2 rounded-full overflow-hidden cursor-pointer"
          style={{ backgroundColor: 'var(--chart-1)' }}
          onMouseMove={handleBarHover}
        >
          {incidentPositions.map((item) => (
            <div
              key={item.index}
              className="absolute top-0 h-full cursor-pointer z-10 transition hover:brightness-110"
              style={{
                left: `${item.start}%`,
                width: `${Math.max(item.width, 0.2)}%`,
                backgroundColor: '#FF3B30'
              }}
            />
          ))}
        </div>
      </div>

      <div className="flex justify-between items-center mt-2 text-sm text-muted-foreground">
        <div>{getRangeLabel()}</div>
        {(uptimeData ? Number(uptimeData.totalChecks) : totalChecks) > 0 && (
          <div className="text-xs">{uptimeData ? Number(uptimeData.totalChecks) : totalChecks} checks</div>
        )}
        <div>Now</div>
      </div>
    </div>
  );
}

export const UptimeBar = memo(UptimeBarInner);