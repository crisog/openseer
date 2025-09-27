
import React from 'react';
import { useState, useEffect, useCallback } from 'react';
import { Monitor, Plus, RefreshCw } from 'lucide-react';
import { useQuery, useMutation } from '@connectrpc/connect-query';
import { useRouter } from "@tanstack/react-router";
import { useSession } from '@/lib/auth-client';

import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';

import { MonitorCard } from './MonitorCard';
import { CreateMonitorForm } from './CreateMonitorForm';
import { RefreshControl } from './RefreshControl';
import { useRefresh } from './MonitorsContent';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';
import type { Monitor as PbMonitor } from '@/lib/gen/openseer/v1/monitors_pb';

export function MonitorsList(): React.JSX.Element {
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [deletingMonitors, setDeletingMonitors] = useState<Set<string>>(new Set());
  const { refreshInterval, setRefreshInterval, triggerRefresh, isRefreshing } = useRefresh();

  const { data: session } = useSession();
  const userId = session?.user?.id;

  const listMonitorsQuery = useQuery(
    MonitorsService.method.listMonitors,
    undefined,
    {
      enabled: !!userId,
    }
  );

  const deleteMonitorMutation = useMutation(MonitorsService.method.deleteMonitor);

  const monitors = (listMonitorsQuery.data?.monitors ?? []) as PbMonitor[];
  const isLoading = listMonitorsQuery.isPending;

  const handleRefresh = useCallback(async () => {
    triggerRefresh();
    await listMonitorsQuery.refetch();
  }, [listMonitorsQuery, triggerRefresh]);

  const handleDelete = useCallback(async (id: string) => {
    setDeletingMonitors(prev => new Set(prev).add(id));
    try {
      await deleteMonitorMutation.mutateAsync({ id });
      await listMonitorsQuery.refetch();
    } catch (error) {
      console.error('Failed to delete monitor:', error);
    } finally {
      setDeletingMonitors(prev => {
        const newSet = new Set(prev);
        newSet.delete(id);
        return newSet;
      });
    }
  }, [deleteMonitorMutation, listMonitorsQuery]);

  useEffect(() => {
    if (refreshInterval === null) return;

    const interval = setInterval(() => {
      handleRefresh();
    }, refreshInterval);

    return () => clearInterval(interval);
  }, [refreshInterval, handleRefresh]);

  return (
    <div className="space-y-4 sm:space-y-6">
      <div className="flex flex-col space-y-3 sm:flex-row sm:items-center sm:justify-between sm:space-y-0">
        <div className="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
          <h2 className="text-lg font-semibold">Monitors</h2>
          <Dialog open={createDialogOpen} onOpenChange={setCreateDialogOpen}>
            <DialogTrigger asChild>
              <Button size="sm" className="flex items-center gap-2 w-full sm:w-auto">
                <Plus className="w-3 h-3" />
                Add Monitor
              </Button>
            </DialogTrigger>
            <DialogContent className="surface max-w-2xl max-h-[90vh] overflow-y-auto">
              <DialogHeader>
                <DialogTitle>Create New Monitor</DialogTitle>
              </DialogHeader>
              <CreateMonitorForm onClose={() => setCreateDialogOpen(false)} />
            </DialogContent>
          </Dialog>
        </div>
        <div className="w-full sm:w-auto">
          <RefreshControl
            onRefresh={handleRefresh}
            refreshInterval={refreshInterval}
            onRefreshIntervalChange={setRefreshInterval}
            isRefreshing={isRefreshing || isLoading}
          />
        </div>
      </div>

      {/* Loading State */}
      {isLoading && monitors.length === 0 && (
        <div className="flex items-center justify-center py-12">
          <div className="text-center space-y-3">
            <RefreshCw className="w-8 h-8 mx-auto animate-spin text-muted-foreground" />
            <p className="text-sm text-muted-foreground">Loading monitors...</p>
          </div>
        </div>
      )}

      {/* Empty State */}
      {!isLoading && monitors.length === 0 && (
        <div className="text-center py-12 space-y-6">
          <div className="w-24 h-24 mx-auto rounded-full bg-secondary/20 flex items-center justify-center">
            <Monitor className="w-12 h-12 text-muted-foreground" />
          </div>
          <div>
            <h3 className="text-xl font-semibold mb-2">No monitors yet</h3>
            <p className="text-muted-foreground mb-6">
              Create your first monitor to start tracking uptime and performance
            </p>
          </div>
          <Button 
            onClick={() => setCreateDialogOpen(true)} 
            size="lg" 
            className="monitor-glow"
          >
            <Plus className="w-5 h-5 mr-2" />
            Create your first monitor
          </Button>
        </div>
      )}

      {/* Monitors Grid */}
      {monitors.length > 0 && (
        <div className="grid gap-3 grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {monitors.map((monitor) => (
            <MonitorCard
              key={monitor.id}
              monitor={{
                id: monitor.id,
                name: monitor.name || monitor.url,
                url: monitor.url,
                method: monitor.method || "GET",
                expectedStatus: 200,
                intervalMs: monitor.intervalMs,
                enabled: monitor.enabled
              }}
              onTogglePause={(id) => {
                console.log('Toggle pause:', id);
              }}
              onDelete={handleDelete}
              isDeleting={deletingMonitors.has(monitor.id)}
              onConfigure={(id) => {
                console.log('Configure:', id);
              }}
              uptimePercentage={99.5}
            />
          ))}
        </div>
      )}
    </div>
  );
}