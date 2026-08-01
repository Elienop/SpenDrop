import { ArrowUpCircle } from 'lucide-react';
import { useServerVersion } from '@/hooks/useServerVersion';
import { BUNDLE_VERSION, isBundleStale } from '@/lib/app-version';
import { cn } from '@/lib/utils';

/**
 * The build stamp: which bundle this device is running, and whether the
 * server has moved on.
 *
 * WHY IT EXISTS. On the installed PWA the service worker serves the
 * previously-cached shell for one launch after a deploy, so "the server was
 * updated" and "my phone is running the new code" routinely disagree. Without
 * a stamp there was no way to tell them apart from the UI, which made every
 * "is this fixed yet?" unanswerable.
 *
 * WHY IT IS QUIET. This is reference information the user goes looking for,
 * not something demanding action, so a stale bundle changes the line's weight
 * (muted → foreground, plus an icon) and nothing else. No toast, no alert, no
 * reload button: the remedy is to relaunch the app, which the SW already does
 * on its own schedule, and a modal nag on the capture screen would cost more
 * than the staleness does.
 */
export function AppVersion({ className }: { className?: string }) {
  const { serverVersion } = useServerVersion();

  // Carry the VALUE, not a boolean. `isBundleStale` only returns true for a
  // non-null server version, but the compiler cannot see that through the
  // call — keeping the string itself narrows honestly instead of asserting
  // non-null, and it is the string the stale line has to render.
  const availableVersion = isBundleStale(BUNDLE_VERSION, serverVersion)
    ? serverVersion
    : null;

  return (
    <p
      data-testid="app-version"
      className={cn(
        'flex items-center gap-1.5 text-xs',
        availableVersion ? 'font-medium text-foreground' : 'text-muted-foreground',
        className,
      )}
    >
      {availableVersion && (
        <ArrowUpCircle className="size-3.5 shrink-0" aria-hidden="true" />
      )}
      <span>
        SpenDrop {BUNDLE_VERSION}
        {/* The server's version is VISIBLE, not parked in a `title`: this
            household's primary surface is a phone, where a title never
            appears at all, and it is unreachable by keyboard and silent to
            screen readers everywhere else.

            "X available" rather than "update available · reload": it stays
            true after a rollback, when the server is the older side and
            nothing newer exists, and it promises nothing about what a reload
            does — one reload still serves the precached shell, so the bundle
            only changes on the launch after the service worker takes
            over. */}
        {availableVersion && ` · ${availableVersion} available`}
      </span>
    </p>
  );
}
