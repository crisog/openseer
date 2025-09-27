'use client';

import { Menu } from 'lucide-react';
import { motion } from 'framer-motion';
import React, { useState, createContext, useContext } from 'react';

import { Sidebar } from './Sidebar';

interface SidebarContextType {
  isOpen: boolean;
  toggle: () => void;
  close: () => void;
}

const SidebarContext = createContext<SidebarContextType | undefined>(undefined);

export const useSidebar = (): SidebarContextType => {
  const context = useContext(SidebarContext);
  if (!context) {
    throw new Error('useSidebar must be used within a SidebarProvider');
  }
  return context;
};

interface MainLayoutProps {
  children: React.ReactNode;
}

export function MainLayout({ children }: MainLayoutProps): React.JSX.Element {
  const [sidebarOpen, setSidebarOpen] = useState(false);

  const sidebarContext: SidebarContextType = {
    isOpen: sidebarOpen,
    toggle: () => setSidebarOpen(!sidebarOpen),
    close: () => setSidebarOpen(false)
  };

  return (
    <SidebarContext.Provider value={sidebarContext}>
      <div className="min-h-screen bg-background text-foreground flex overflow-x-hidden">
        <Sidebar />

        {/* Mobile hamburger menu button */}
        <button
          onClick={sidebarContext.toggle}
          className={`fixed top-3 right-3 z-50 p-3 rounded-lg bg-background/95 backdrop-blur-sm border border-border shadow-lg lg:hidden hover:bg-secondary/50 focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 transition-all duration-200 ease-out ${
            sidebarOpen ? 'opacity-0 pointer-events-none scale-95' : 'opacity-100 scale-100'
          }`}
          aria-label="Toggle sidebar"
          aria-expanded={sidebarOpen}
        >
          <Menu className="w-5 h-5" />
        </button>

        <div className="flex-1 transition-all duration-300 ease-in-out lg:ml-56 pt-4 lg:pt-0 min-w-0">
          <motion.div
            initial={{ opacity: 1, y: 0 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.2, ease: 'easeOut' }}
            className="min-h-screen"
          >
            {children}
          </motion.div>
        </div>
        {sidebarOpen && (
          <div className="fixed inset-0 bg-background/80 backdrop-blur-sm z-40 lg:hidden" onClick={sidebarContext.close} />
        )}
      </div>
    </SidebarContext.Provider>
  );
}
