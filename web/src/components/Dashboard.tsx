import React from 'react';
import { useRouterState } from '@tanstack/react-router';

import { type DashboardTab } from './Sidebar';
import { MonitorsContent } from './monitors/MonitorsContent';

function DashboardContent(): React.JSX.Element {
  const routerState = useRouterState();
  const pathname = routerState.location.pathname;

  const getActiveTabFromPathname = (path: string | null): DashboardTab => {
    if (!path) return 'monitors';
    if (path === '/_dashboard/dashboard' || path === '/') return 'monitors';
    if (path.startsWith('/_dashboard/monitors')) return 'monitors';
    if (path.startsWith('/_dashboard/incidents')) return 'incidents';
    if (path.startsWith('/_dashboard/analytics')) return 'analytics';
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
