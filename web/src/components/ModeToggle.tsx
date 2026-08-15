import { Moon, Sun } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useTheme } from '@/hooks/useTheme';
import { cn } from '@/lib/utils';

interface ModeToggleProps {
  /**
   * Merged onto the trigger. The `icon` size is 40px, which suits a mouse;
   * a touch surface passes `size-11` to reach the 44px floor.
   */
  className?: string;
}

export function ModeToggle({ className }: ModeToggleProps) {
  const { setTheme } = useTheme();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {/* `relative` HERE, not on the Button base (B47, 2026-08-15): the Moon
            below is `absolute`, and `ui/button.tsx` establishes no containing
            block, so without this the glyph resolves against whatever
            positioned ancestor the placement happens to supply — inside the
            mobile drawer that is `SheetContent`, so the icon stops scrolling
            with its own button.

            Scoped to the trigger rather than the base because `relative` on
            the base would turn EVERY button into a containing block and
            silently re-parent any `absolute` descendant a call site has now or
            adds later. The one other Button in the app with a positioned
            descendant, `ui/password-input.tsx`, is itself `absolute` and so
            already establishes the block its `before:absolute` hit area needs
            — it neither needs the base to decide this nor benefits from it.

            Ahead of `className` so a call site can still override the position
            group if one ever needs to. */}
        <Button
          variant="outline"
          size="icon"
          className={cn('relative', className)}
        >
          <Sun className="scale-100 rotate-0 transition-all dark:scale-0 dark:-rotate-90" />
          <Moon className="absolute scale-0 rotate-90 transition-all dark:scale-100 dark:rotate-0" />
          <span className="sr-only">Toggle theme</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => setTheme('light')}>
          Light
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => setTheme('dark')}>
          Dark
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => setTheme('system')}>
          System
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
