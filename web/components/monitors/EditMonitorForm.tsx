'use client';

import React, { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation } from '@connectrpc/connect-query';
import { z } from 'zod';

import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Form, FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Switch } from '@/components/ui/switch';
import { MonitorsService } from '@/lib/gen/openseer/v1/monitors_pb';
import type { Monitor } from '@/lib/gen/openseer/v1/monitors_pb';

const monitorFormSchema = z.object({
  name: z.string().min(1, 'Name is required'),
  url: z.string().url('Must be a valid URL'),
  method: z.enum(['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD']),
  intervalMs: z.number().min(10000).max(86400000),
  timeoutMs: z.number().min(1000).max(60000),
  enabled: z.boolean(),
});

type MonitorFormData = z.infer<typeof monitorFormSchema>;

const intervalOptions = [
  { value: 10000, label: '10 seconds' },
  { value: 30000, label: '30 seconds' },
  { value: 60000, label: '1 minute' },
  { value: 300000, label: '5 minutes' },
  { value: 600000, label: '10 minutes' },
  { value: 1800000, label: '30 minutes' },
  { value: 3600000, label: '1 hour' },
];

const timeoutOptions = [
  { value: 5000, label: '5 seconds' },
  { value: 10000, label: '10 seconds' },
  { value: 30000, label: '30 seconds' },
  { value: 60000, label: '1 minute' },
];

interface EditMonitorFormProps {
  monitor: Monitor;
  onSuccess?: () => void;
  onCancel?: () => void;
}

export function EditMonitorForm({ monitor, onSuccess, onCancel }: EditMonitorFormProps): React.JSX.Element {
  const [isSubmitting, setIsSubmitting] = useState(false);

  const updateMonitorMutation = useMutation(MonitorsService.method.updateMonitor);

  const form = useForm<MonitorFormData>({
    resolver: zodResolver(monitorFormSchema),
    defaultValues: {
      name: monitor.name || '',
      url: monitor.url,
      method: monitor.method as 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH' | 'HEAD',
      intervalMs: monitor.intervalMs,
      timeoutMs: monitor.timeoutMs,
      enabled: monitor.enabled ?? true,
    },
  });

  const onSubmit = async (data: MonitorFormData) => {
    setIsSubmitting(true);
    try {
      await updateMonitorMutation.mutateAsync({
        id: monitor.id,
        name: data.name,
        url: data.url,
        method: data.method,
        intervalMs: data.intervalMs,
        timeoutMs: data.timeoutMs,
        enabled: data.enabled,
      });
      onSuccess?.();
    } catch (error) {
      console.error('Failed to update monitor:', error);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
        <FormField
          control={form.control}
          name="name"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Name</FormLabel>
              <FormControl>
                <Input placeholder="My Monitor" {...field} />
              </FormControl>
              <FormDescription>
                A friendly name for your monitor
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormField
          control={form.control}
          name="url"
          render={({ field }) => (
            <FormItem>
              <FormLabel>URL</FormLabel>
              <FormControl>
                <Input placeholder="https://example.com" {...field} />
              </FormControl>
              <FormDescription>
                The URL to monitor
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <div className="grid grid-cols-2 gap-4">
          <FormField
            control={form.control}
            name="method"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Method</FormLabel>
                <Select onValueChange={field.onChange} defaultValue={field.value}>
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue placeholder="Select method" />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    <SelectItem value="GET">GET</SelectItem>
                    <SelectItem value="POST">POST</SelectItem>
                    <SelectItem value="PUT">PUT</SelectItem>
                    <SelectItem value="DELETE">DELETE</SelectItem>
                    <SelectItem value="PATCH">PATCH</SelectItem>
                    <SelectItem value="HEAD">HEAD</SelectItem>
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="intervalMs"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Check Interval</FormLabel>
                <Select 
                  onValueChange={(value) => field.onChange(Number(value))} 
                  defaultValue={field.value.toString()}
                >
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue placeholder="Select interval" />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {intervalOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value.toString()}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <FormField
            control={form.control}
            name="timeoutMs"
            render={({ field }) => (
              <FormItem>
                <FormLabel>Timeout</FormLabel>
                <Select 
                  onValueChange={(value) => field.onChange(Number(value))} 
                  defaultValue={field.value.toString()}
                >
                  <FormControl>
                    <SelectTrigger>
                      <SelectValue placeholder="Select timeout" />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent>
                    {timeoutOptions.map((option) => (
                      <SelectItem key={option.value} value={option.value.toString()}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name="enabled"
            render={({ field }) => (
              <FormItem className="flex flex-row items-center justify-between rounded-lg border p-3">
                <div className="space-y-0.5">
                  <FormLabel>Enabled</FormLabel>
                  <FormDescription>
                    Monitor is active
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />
        </div>

        <div className="flex justify-end gap-3 pt-4">
          <Button type="button" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? 'Updating...' : 'Update Monitor'}
          </Button>
        </div>
      </form>
    </Form>
  );
}