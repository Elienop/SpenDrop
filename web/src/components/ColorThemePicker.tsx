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

export function ColorThemePicker() {
  const { colorTheme, setColorTheme } = useTheme();
  const active = colorTheme ?? 'neutral';

  return (
    <Select value={active} onValueChange={(v) => setColorTheme(v as ColorThemeId)}>
      <SelectTrigger className="h-8 w-full text-xs">
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
