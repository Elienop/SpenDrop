import { useTheme } from '@/hooks/useTheme';
import { type ColorThemeId, colorThemes, colorThemeIds } from '@/lib/color-themes';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';

interface ColorThemePickerProps {
  /**
   * Merged onto the trigger. The 32px default suits the desktop sidebar's
   * dense footer; a touch surface passes `h-11` to reach the 44px floor.
   */
  className?: string;
}

export function ColorThemePicker({ className }: ColorThemePickerProps) {
  const { colorTheme, setColorTheme } = useTheme();
  const active = colorTheme ?? 'neutral';

  return (
    <Select value={active} onValueChange={(v) => setColorTheme(v as ColorThemeId)}>
      <SelectTrigger className={cn('h-8 w-full text-xs', className)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {colorThemeIds.map((id) => (
            <SelectItem key={id} value={id}>
              {colorThemes[id].label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  );
}
