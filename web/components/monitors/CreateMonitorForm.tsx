'use client';

import React, { useMemo, useState } from 'react';
import { createConnectQueryKey, useMutation, useQuery, useTransport } from '@connectrpc/connect-query';
import { useQueryClient } from '@tanstack/react-query';
import { Loader2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';

import { DashboardService } from '@/lib/gen/openseer/v1/dashboard_pb';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';

const normalizeRegion = (value: string): string => value.trim().toLowerCase();

const normalizeRegionList = (values: string[]): string[] => {
  if (values.length === 0) {
    return values;
  }
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const normalized = normalizeRegion(value);
    if (!normalized || seen.has(normalized)) {
      continue;
    }
    seen.add(normalized);
    result.push(normalized);
  }
  return result;
};

interface CreateMonitorFormProps {
  onClose: () => void;
}

export function CreateMonitorForm({ onClose }: CreateMonitorFormProps): React.JSX.Element {
  const [creating, setCreating] = useState(false);
  const [newMonitorName, setNewMonitorName] = useState('');
  const [newMonitorUrl, setNewMonitorUrl] = useState('');
  const [newMonitorInterval, setNewMonitorInterval] = useState('60000');
  const [newMonitorMethod, setNewMonitorMethod] = useState('GET');
  const [regionScope, setRegionScope] = useState<'any' | 'specific'>('any');
  const [selectedRegions, setSelectedRegions] = useState<string[]>([]);
  const [customRegion, setCustomRegion] = useState('');

  const createMonitorMutation = useMutation(MonitorsService.method.createMonitor);
  const queryClient = useQueryClient();
  const transport = useTransport();
  const regionHealthQuery = useQuery(DashboardService.method.getRegionHealth, undefined, {
    retry: false,
  });

  const knownRegions = regionHealthQuery.data?.regions ?? [];
  const regionHealthMap = useMemo(() => {
    const map = new Map<string, number>();
    for (const region of knownRegions) {
      map.set(normalizeRegion(region.region), Number(region.healthyWorkers ?? 0));
    }
    return map;
  }, [knownRegions]);

  const zeroHealthySelections = useMemo(() => {
    if (regionScope !== 'specific') {
      return [];
    }
    const normalized = normalizeRegionList(selectedRegions);
    return normalized.filter((region) => (regionHealthMap.get(region) ?? 0) === 0);
  }, [regionScope, selectedRegions, regionHealthMap]);

  const createMonitor = async () => {
    setCreating(true);
    try {
      const normalizedRegions = regionScope === 'specific' ? normalizeRegionList(selectedRegions) : [];

      if (regionScope === 'specific' && normalizedRegions.length === 0) {
        alert('Select at least one region when using specific regions.');
        return;
      }

      if (regionScope === 'specific' && zeroHealthySelections.length > 0) {
        const confirmed = window.confirm(
          `No healthy workers are currently connected in: ${zeroHealthySelections.join(', ')}. Create monitor anyway?`,
        );
        if (!confirmed) {
          return;
        }
      }

      const monitorId = `monitor-${Date.now()}`;
      await createMonitorMutation.mutateAsync({
        id: monitorId,
        name: newMonitorName || newMonitorUrl,
        url: newMonitorUrl,
        intervalMs: parseInt(newMonitorInterval),
        timeoutMs: 5000,
        method: newMonitorMethod,
        enabled: true,
        regions: regionScope === 'specific' ? normalizedRegions : undefined,
      });

      await queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: MonitorsService.method.listMonitors,
          transport,
          cardinality: 'finite',
          input: undefined,
        }),
      });

      setNewMonitorName('');
      setNewMonitorUrl('');
      setNewMonitorInterval('60000');
      setNewMonitorMethod('GET');
      setRegionScope('any');
      setSelectedRegions([]);
      onClose();
    } catch (error) {
      console.error('Error creating monitor:', error);
      alert('Failed to create monitor. Please try again.');
    } finally {
      setCreating(false);
    }
  };

  const toggleRegion = (region: string) => {
    const normalized = normalizeRegion(region);
    if (!normalized) {
      return;
    }
    setSelectedRegions((prev) =>
      prev.includes(normalized) ? prev.filter((value) => value !== normalized) : [...prev, normalized],
    );
  };

  const handleAddCustomRegion = () => {
    const normalized = normalizeRegion(customRegion);
    if (!normalized) {
      return;
    }
    setSelectedRegions((prev) => (prev.includes(normalized) ? prev : [...prev, normalized]));
    setCustomRegion('');
  };

  const removeRegion = (region: string) => {
    setSelectedRegions((prev) => prev.filter((value) => value !== region));
  };

  const selectedRegionSet = useMemo(() => new Set(selectedRegions), [selectedRegions]);

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="name">Name (optional)</Label>
          <Input
            id="name"
            value={newMonitorName}
            onChange={(e) => setNewMonitorName(e.target.value)}
            placeholder="My Website"
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="url">URL</Label>
          <Input
            id="url"
            value={newMonitorUrl}
            onChange={(e) => setNewMonitorUrl(e.target.value)}
            placeholder="https://example.com"
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label htmlFor="interval">Interval</Label>
            <Select value={newMonitorInterval} onValueChange={setNewMonitorInterval}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="30000">30 seconds</SelectItem>
                <SelectItem value="60000">1 minute</SelectItem>
                <SelectItem value="300000">5 minutes</SelectItem>
                <SelectItem value="600000">10 minutes</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label htmlFor="method">Method</Label>
            <Select value={newMonitorMethod} onValueChange={setNewMonitorMethod}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="GET">GET</SelectItem>
                <SelectItem value="POST">POST</SelectItem>
                <SelectItem value="HEAD">HEAD</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="space-y-2">
          <Label>Region scope</Label>
          <div className="flex items-center gap-4">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="regionScope"
                value="any"
                checked={regionScope === 'any'}
                onChange={() => {
                  setRegionScope('any');
                  setSelectedRegions([]);
                  setCustomRegion('');
                }}
              />
              <span>Any region</span>
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="regionScope"
                value="specific"
                checked={regionScope === 'specific'}
                onChange={() => setRegionScope('specific')}
              />
              <span>Specific regions</span>
            </label>
          </div>
        </div>

        {regionScope === 'specific' && (
          <div className="space-y-3 rounded-md border border-border p-4">
            {regionHealthQuery.isLoading && <p className="text-sm text-muted-foreground">Loading regions…</p>}
            {regionHealthQuery.isError && (
              <p className="text-sm text-destructive">
                Failed to load regions. You can still add custom regions manually.
              </p>
            )}

            {knownRegions.length > 0 ? (
              <div className="space-y-2">
                {knownRegions.map((region) => {
                  const normalized = normalizeRegion(region.region);
                  const healthy = Number(region.healthyWorkers ?? 0);
                  return (
                    <label key={region.region} className="flex items-center justify-between rounded-md border border-transparent px-2 py-1 text-sm hover:border-border">
                      <span className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          checked={selectedRegionSet.has(normalized)}
                          onChange={() => toggleRegion(region.region)}
                        />
                        <span className="font-medium">{region.region}</span>
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {healthy === 0
                          ? 'No healthy workers'
                          : healthy === 1
                            ? '1 healthy worker'
                            : `${healthy} healthy workers`}
                      </span>
                  </label>
                  );
                })}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">
                No regions discovered yet. Add a region manually to continue.
              </p>
            )}

            <div className="flex items-center gap-2">
              <Input
                value={customRegion}
                onChange={(event) => setCustomRegion(event.target.value)}
                placeholder="Add custom region (e.g. us-west-2)"
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    handleAddCustomRegion();
                  }
                }}
              />
              <Button type="button" variant="outline" onClick={handleAddCustomRegion}>
                Add
              </Button>
            </div>

            {selectedRegions.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {selectedRegions.map((region) => (
                  <span
                    key={region}
                    className="flex items-center gap-1 rounded-full bg-muted px-3 py-1 text-xs font-medium"
                  >
                    {region}
                    <button
                      type="button"
                      onClick={() => removeRegion(region)}
                      className="text-muted-foreground transition hover:text-foreground"
                      aria-label={`Remove ${region}`}
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            )}

            {zeroHealthySelections.length > 0 && (
              <div className="rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
                No healthy workers currently available in: {zeroHealthySelections.join(', ')}
              </div>
            )}
          </div>
        )}
      </div>

      <div className="flex justify-end space-x-2 pt-4">
        <Button variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button onClick={createMonitor} disabled={creating || !newMonitorUrl}>
          {creating ? (
            <>
              <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              Creating...
            </>
          ) : (
            'Create Monitor'
          )}
        </Button>
      </div>
    </div>
  );
}
