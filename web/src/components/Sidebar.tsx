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
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import { ModeToggle } from '@/components/ModeToggle';
import { ColorThemePicker } from '@/components/ColorThemePicker';
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
          'fixed left-0 top-0 flex h-screen flex-col border-r border-border bg-card transition-[width] duration-200 ease-linear',
          expanded ? 'w-60' : 'w-12',
        )}
      >
        {/* Header — matches SidebarHeader: gap-2 p-2 */}
        <div
          className={cn(
            'flex h-14 shrink-0 items-center border-b border-border p-2',
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

        {/* Content — matches SidebarContent: gap-2, overflow-hidden when collapsed */}
        <nav
          className={cn(
            'flex min-h-0 flex-1 flex-col gap-2',
            expanded ? 'overflow-auto' : 'overflow-hidden',
          )}
          aria-label="Primary"
        >
          {/* Menu group — matches SidebarGroup: p-2 */}
          <div className={cn('flex flex-col gap-0.5 p-2', !expanded && 'items-center')}>
            <SidebarSectionTitle expanded={expanded} title="Menu" />
            {menuItems.map((item) => (
              <SidebarLink key={item.path} item={item} expanded={expanded} />
            ))}
          </div>

          {/* General group */}
          <div className={cn('flex flex-col gap-0.5 p-2', !expanded && 'items-center')}>
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
                    'flex items-center overflow-hidden rounded-md text-sm text-muted-foreground hover:bg-muted hover:text-foreground [&>svg]:size-4 [&>svg]:shrink-0',
                    expanded ? 'w-full gap-2 px-3 py-2' : '!size-8 p-2 justify-center',
                  )}
                >
                  <LogOut aria-hidden="true" />
                  <span className={expanded ? 'truncate' : 'sr-only'}>
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

        {/* Color theme select — above footer */}
        {expanded && (
          <div className="px-3 pb-2">
            <ColorThemePicker />
          </div>
        )}

        {/* Footer — matches SidebarFooter: gap-2 p-2 */}
        <div
          className={cn(
            'flex items-center border-t border-border p-2',
            expanded ? 'gap-3 px-3 py-3' : 'flex-col gap-2 py-3',
          )}
        >
          <Avatar className="size-8 text-sm font-medium">
            <AvatarFallback>{initial}</AvatarFallback>
          </Avatar>
          {expanded && user && (
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">
                {user.display_name}
              </p>
              <p className="truncate text-xs text-muted-foreground">
                @{user.username}
              </p>
            </div>
          )}
          <ModeToggle />
        </div>
      </aside>
    </TooltipProvider>
  );
}

function SidebarSectionTitle({
  title,
  expanded,
}: {
  title: string;
  expanded: boolean;
}) {
  return (
    <p
      className={cn(
        'h-8 px-3 text-[10px] font-medium uppercase tracking-wider text-muted-foreground transition-[margin,opacity] duration-200 ease-linear',
        expanded ? 'pb-1' : '-mt-8 opacity-0',
      )}
    >
      {title}
    </p>
  );
}

function SidebarLink({
  item,
  expanded,
}: {
  item: {
    path: string;
    label: string;
    icon: React.ElementType;
    end?: boolean;
  };
  expanded: boolean;
}) {
  const Icon = item.icon;
  const link = (
    <NavLink
      to={item.path}
      end={item.end}
      className={({ isActive }) =>
        cn(
          'flex items-center overflow-hidden rounded-md text-sm text-muted-foreground hover:bg-muted hover:text-foreground [&>svg]:size-4 [&>svg]:shrink-0',
          isActive && 'bg-muted text-foreground',
          expanded ? 'w-full gap-2 px-3 py-2' : '!size-8 p-2 justify-center',
        )
      }
    >
      <Icon aria-hidden="true" />
      <span className={expanded ? 'truncate' : 'sr-only'}>
        {item.label}
      </span>
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
