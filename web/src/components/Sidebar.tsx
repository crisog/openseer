import React, { useRef, useEffect } from 'react';
import { useRouter, useRouterState } from '@tanstack/react-router';
import { AlertTriangle, Monitor, Settings, BarChart3, HelpCircle, LogOut } from 'lucide-react';

import { Separator } from '@/components/ui/separator';

import { useSidebar } from './MainLayout';

export type DashboardTab = 'monitors' | 'incidents' | 'analytics' | 'billing';

const navigationItems = [
  { icon: Monitor, label: 'Monitors', key: 'monitors' as DashboardTab },
  { icon: AlertTriangle, label: 'Incidents', key: 'incidents' as DashboardTab, disabled: true },
  { icon: BarChart3, label: 'Reporting', key: 'analytics' as DashboardTab, disabled: true }
];

const bottomItems = [
  { icon: HelpCircle, label: 'Help', key: 'help', external: true }
];

export function Sidebar(): React.JSX.Element {
  const router = useRouter();
  const routerState = useRouterState();
  const pathname = routerState.location.pathname;
  const { isOpen, close } = useSidebar();
  const sidebarRef = useRef<HTMLElement>(null);
  const startXRef = useRef<number>(0);
  const currentXRef = useRef<number>(0);

  const handleTouchStart = (e: React.TouchEvent): void => {
    startXRef.current = e.touches[0].clientX;
    currentXRef.current = e.touches[0].clientX;
  };

  const handleTouchMove = (e: React.TouchEvent): void => {
    currentXRef.current = e.touches[0].clientX;
  };

  const handleTouchEnd = (): void => {
    const deltaX = currentXRef.current - startXRef.current;
    const threshold = 100;

    if (deltaX < -threshold && isOpen) {
      close();
    }
  };

  useEffect(() => {
    const handleEscape = (e: KeyboardEvent): void => {
      if (e.key === 'Escape' && isOpen) {
        close();
      }
    };

    document.addEventListener('keydown', handleEscape);
    return (): void => document.removeEventListener('keydown', handleEscape);
  }, [isOpen, close]);

  const getCurrentActiveTab = (path: string): DashboardTab => {
    if (path === '/dashboard' || path === '/monitors' || path.startsWith('/monitors/')) return 'monitors';
    if (path.startsWith('/incidents')) return 'incidents';
    if (path.startsWith('/analytics')) return 'analytics';
    return 'monitors';
  };

  const currentActiveTab = getCurrentActiveTab(pathname);

  const handleTabClick = async (tab: string, disabled?: boolean, external?: boolean): Promise<void> => {
    if (disabled) return;
    if (external) return;

    close();

    setTimeout(() => {
      if (tab === 'monitors') {
        router.navigate({ to: '/monitors' });
      } else {
        router.navigate({ to: `/${tab}` });
      }
    }, 150);
  };

  const handleLogout = async (): Promise<void> => {
    router.navigate({ to: '/' });
  };

  const renderNavItem = (
    item: {
      icon: React.ComponentType<{ className?: string }>;
      label: string;
      key: string;
      disabled?: boolean;
      external?: boolean;
    },
    isActive: boolean
  ): React.JSX.Element => (
    <button
      key={item.label}
      onClick={() => handleTabClick(item.key, item.disabled, item.external)}
      aria-current={isActive ? 'page' : undefined}
      disabled={item.disabled}
      className={`
        w-full flex items-center space-x-3 px-3 py-3 sm:py-2 rounded-md cursor-pointer
        transition-all duration-200 ease-out select-none text-sm min-h-[44px] sm:min-h-[36px]
        focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background
        ${
          item.disabled
            ? 'opacity-50 cursor-not-allowed hover:bg-transparent'
            : isActive
              ? 'bg-primary/10 text-primary border border-primary/20'
              : 'hover:bg-secondary/40 hover:text-foreground active:bg-secondary/60'
        }
      `}
    >
      <item.icon className={`w-4 h-4 ${isActive ? 'text-primary' : 'text-muted-foreground'}`} />
      <span className="font-medium">{item.label}</span>
      {item.disabled && <span className="ml-auto text-xs text-muted-foreground">Soon</span>}
    </button>
  );

  return (
    <aside
      ref={sidebarRef}
      onTouchStart={handleTouchStart}
      onTouchMove={handleTouchMove}
      onTouchEnd={handleTouchEnd}
      className={`
        fixed left-0 top-0 w-72 sm:w-64 md:w-56 surface border-r h-screen overflow-y-auto z-50
        transition-transform duration-300 ease-in-out flex flex-col
        ${isOpen ? 'translate-x-0' : '-translate-x-full'}
        lg:translate-x-0 lg:w-56
      `}
    >
      <div className="p-3 sm:p-4 border-b border-border/40">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-primary flex items-center justify-center">
            <Monitor className="w-4 h-4 text-primary-foreground" />
          </div>
          <div>
            <h2 className="font-semibold text-foreground">OpenSeer</h2>
            <p className="text-xs text-muted-foreground">Uptime Monitoring</p>
          </div>
        </div>
      </div>

      <nav className="flex-1 p-2 sm:p-3 space-y-0.5">
        {navigationItems.map((item) => {
          const isActive = currentActiveTab === item.key && !item.disabled;
          return renderNavItem(item, isActive);
        })}
      </nav>

      <div className="border-t border-border/40 p-2 sm:p-3 space-y-0.5">
        {bottomItems.map((item) => {
          const isActive = currentActiveTab === item.key;
          return renderNavItem(item, isActive);
        })}

        <Separator className="my-2" />

        <button
          onClick={handleLogout}
          className="w-full flex items-center space-x-3 px-3 py-3 sm:py-2 rounded-md hover:bg-secondary/40 text-sm text-muted-foreground hover:text-foreground transition-colors min-h-[44px] sm:min-h-[36px] active:bg-secondary/60"
        >
          <LogOut className="w-4 h-4" />
          <span>Logout</span>
        </button>
      </div>
    </aside>
  );
}
