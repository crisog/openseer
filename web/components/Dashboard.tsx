'use client';

import React from 'react';
import { usePathname } from 'next/navigation';

import { type DashboardTab } from './Sidebar';
import { MonitorsContent } from './monitors/MonitorsContent';

function DashboardContent(): React.JSX.Element {
  const pathname = usePathname();

  const getActiveTabFromPathname = (path: string): DashboardTab => {
    if (path === '/dashboard' || path === '/') return 'monitors';
    if (path.startsWith('/dashboard/monitors')) return 'monitors';
    if (path.startsWith('/dashboard/incidents')) return 'incidents';
    if (path.startsWith('/dashboard/analytics')) return 'analytics';
    if (path.startsWith('/dashboard/settings')) return 'settings';
    return 'monitors';
  };

  const activeTab = getActiveTabFromPathname(pathname);

  const renderContent = (): React.JSX.Element => {
    switch (activeTab) {
      case 'monitors':
        return <MonitorsContent />;
      default:
        return <MonitorsContent />;
    }
  };

  return (
    <div className="w-full">{renderContent()}</div>
  );
}

export function Dashboard(): React.JSX.Element {
  return <DashboardContent />;
}
