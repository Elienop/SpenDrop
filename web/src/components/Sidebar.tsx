import { useState, useEffect, useRef } from 'react';
import { NavLink } from 'react-router-dom';
import {
  LayoutGrid,
  ArrowLeftRight,
  ChartNoAxesColumnIncreasing,
  Tag,
  Settings as SettingsIcon,
  ChevronLeft,
  ChevronRight,
  LogOut,
} from 'lucide-react';
import { useAuth } from '../hooks/useAuth';
import { ScrollArea } from '@/components/ui/scroll-area';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

const menuItems = [
  { path: '/', label: 'Dashboard', icon: LayoutGrid, end: true },
  { path: '/transactions', label: 'Transactions', icon: ArrowLeftRight },
  { path: '/reports', label: 'Reports', icon: ChartNoAxesColumnIncreasing },
  { path: '/categories', label: 'Categories', icon: Tag },
];

const generalItems = [
  { path: '/settings', label: 'Settings', icon: SettingsIcon },
];

export function Sidebar() {
  const { user, logout } = useAuth();
  const [expanded, setExpanded] = useState(
    () => localStorage.getItem('spendrop-sidebar') === 'true',
  );

  const didMountRef = useRef(false);
  useEffect(() => {
    if (!didMountRef.current) {
      didMountRef.current = true;
      return;
    }
    localStorage.setItem('spendrop-sidebar', String(expanded));
    window.dispatchEvent(new Event('sidebar-toggle'));
  }, [expanded]);

  const initial = user?.display_name?.[0]?.toUpperCase() ?? '?';

  return (
    <TooltipProvider delayDuration={200}>
      <aside
        role="complementary"
        className={cn(
          'fixed left-0 top-0 flex h-screen flex-col border-r border-border bg-card transition-[width] duration-150',
          expanded ? 'w-60' : 'w-16',
        )}
      >
        <div
          className={cn(
            'flex h-14 items-center border-b border-border',
            expanded ? 'justify-between px-4' : 'justify-center',
          )}
        >
          {expanded ? (
            <span className="font-semibold tracking-tight">SpenDrop</span>
          ) : (
            <span className="sr-only">SpenDrop</span>
          )}
          <button
            type="button"
            aria-label="Toggle sidebar"
            aria-expanded={expanded}
            onClick={() => setExpanded((v) => !v)}
            className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            {expanded ? (
              <ChevronLeft className="h-4 w-4" />
            ) : (
              <ChevronRight className="h-4 w-4" />
            )}
          </button>
        </div>

        <ScrollArea className="flex-1">
          <nav className="flex flex-col gap-6 px-2 py-4" aria-label="Primary">
            <SidebarSection
              title="Menu"
              items={menuItems}
              expanded={expanded}
            />
            <div className="flex flex-col gap-1">
              <SidebarSectionTitle expanded={expanded} title="General" />
              {generalItems.map((item) => (
                <SidebarLink key={item.path} item={item} expanded={expanded} />
              ))}
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={() => void logout()}
                    className={cn(
                      'flex items-center rounded-md text-sm text-muted-foreground hover:bg-muted hover:text-foreground',
                      expanded
                        ? 'mx-1 gap-3 px-3 py-2'
                        : 'mx-auto h-9 w-9 justify-center',
                    )}
                  >
                    <LogOut className="h-4 w-4 shrink-0" aria-hidden="true" />
                    <span className={expanded ? undefined : 'sr-only'}>
                      Log out
                    </span>
                  </button>
                </TooltipTrigger>
                {!expanded && (
                  <TooltipContent side="right">Log out</TooltipContent>
                )}
              </Tooltip>
            </div>
          </nav>
        </ScrollArea>

        <div
          className={cn(
            'flex items-center border-t border-border py-3',
            expanded ? 'gap-3 px-3' : 'justify-center',
          )}
        >
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted text-sm font-medium">
            {initial}
          </div>
          {expanded && user && (
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">
                {user.display_name}
              </p>
              <p className="truncate text-xs text-muted-foreground">
                @{user.username}
              </p>
            </div>
          )}
        </div>
      </aside>
    </TooltipProvider>
  );
}

function SidebarSection({
  title,
  items,
  expanded,
}: {
  title: string;
  items: typeof menuItems;
  expanded: boolean;
}) {
  return (
    <div className="flex flex-col gap-1">
      <SidebarSectionTitle expanded={expanded} title={title} />
      {items.map((item) => (
        <SidebarLink key={item.path} item={item} expanded={expanded} />
      ))}
    </div>
  );
}

function SidebarSectionTitle({
  title,
  expanded,
}: {
  title: string;
  expanded: boolean;
}) {
  if (!expanded) return null;
  return (
    <p className="px-3 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
      {title}
    </p>
  );
}

function SidebarLink({
  item,
  expanded,
}: {
  item: { path: string; label: string; icon: React.ElementType; end?: boolean };
  expanded: boolean;
}) {
  const Icon = item.icon;
  const link = (
    <NavLink
      to={item.path}
      end={item.end}
      className={({ isActive }) =>
        cn(
          'flex items-center rounded-md text-sm text-muted-foreground hover:bg-muted hover:text-foreground',
          isActive && 'bg-muted text-foreground',
          expanded
            ? 'mx-1 gap-3 px-3 py-2'
            : 'mx-auto h-9 w-9 justify-center',
        )
      }
    >
      <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
      <span className={expanded ? undefined : 'sr-only'}>{item.label}</span>
    </NavLink>
  );
  if (expanded) return link;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  );
}
