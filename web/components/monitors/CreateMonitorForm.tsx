'use client';

import React, { useState } from 'react';
import { createConnectQueryKey, useMutation, useTransport } from '@connectrpc/connect-query';
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

import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';

interface CreateMonitorFormProps {
  onClose: () => void;
}

export function CreateMonitorForm({ onClose }: CreateMonitorFormProps): React.JSX.Element {
  const [creating, setCreating] = useState(false);
  const [newMonitorName, setNewMonitorName] = useState('');
  const [newMonitorUrl, setNewMonitorUrl] = useState('');
  const [newMonitorInterval, setNewMonitorInterval] = useState('60000');
  const [newMonitorMethod, setNewMonitorMethod] = useState('GET');

  const createMonitorMutation = useMutation(MonitorsService.method.createMonitor);
  const queryClient = useQueryClient();
  const transport = useTransport();

  const createMonitor = async () => {
    setCreating(true);
    try {
      const monitorId = `monitor-${Date.now()}`;
      await createMonitorMutation.mutateAsync({
        id: monitorId,
        name: newMonitorName || newMonitorUrl,
        url: newMonitorUrl,
        intervalMs: parseInt(newMonitorInterval),
        timeoutMs: 5000,
        method: newMonitorMethod,
        enabled: true,
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
      onClose();
    } catch (error) {
      console.error('Error creating monitor:', error);
      alert('Failed to create monitor. Please try again.');
    } finally {
      setCreating(false);
    }
  };

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
