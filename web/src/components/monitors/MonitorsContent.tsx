
import React, { useState, createContext, useContext, useCallback, Suspense } from 'react';
import { MonitorsList } from './MonitorsList';
import { RefreshStatusIndicator } from './RefreshStatusIndicator';

interface RefreshContextType {
  refreshInterval: number | null;
  setRefreshInterval: (interval: number | null) => void;
  lastRefresh: Date | null;
  isRefreshing: boolean;
  triggerRefresh: () => void;
}

const RefreshContext = createContext<RefreshContextType | undefined>(undefined);

export function useRefresh(): RefreshContextType {
  const context = useContext(RefreshContext);
  if (!context) {
    throw new Error('useRefresh must be used within RefreshProvider');
  }
  return context;
}

function MonitorsContentInner(): React.JSX.Element {
  const { refreshInterval, lastRefresh, isRefreshing } = useRefresh();

  return (
    <div className="relative min-h-screen">
      <div className="p-6 sm:p-8 lg:p-10 space-y-6 pt-8 sm:pt-10">
        <MonitorsList />
      </div>

      {lastRefresh && (
        <div className="absolute bottom-2 right-2 z-10">
          <div className="surface rounded-md px-2 py-1 shadow-lg">
            <RefreshStatusIndicator refreshInterval={refreshInterval} lastRefresh={lastRefresh} isRefreshing={isRefreshing} />
          </div>
        </div>
      )}
    </div>
  );
}

export function MonitorsContent(): React.JSX.Element {
  const [refreshInterval, setRefreshInterval] = useState<number | null>(null);
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const [isRefreshing, setIsRefreshing] = useState(false);

  const triggerRefresh = useCallback(() => {
    setIsRefreshing(true);
    setLastRefresh(new Date());
    setTimeout(() => setIsRefreshing(false), 500);
  }, []);

  const contextValue: RefreshContextType = {
    refreshInterval,
    setRefreshInterval,
    lastRefresh,
    isRefreshing,
    triggerRefresh
  };

  return (
    <RefreshContext.Provider value={contextValue}>
      <Suspense fallback={<div className="p-6 sm:p-8 lg:p-10"><div className="animate-pulse bg-secondary/20 rounded-lg h-40 w-full"></div></div>}>
        <MonitorsContentInner />
      </Suspense>
    </RefreshContext.Provider>
  );
}
