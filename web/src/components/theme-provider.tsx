import { useEffect, useState } from 'react';
import {
  type ColorThemeId,
  colorThemes,
  applyColorTheme,
  clearColorThemeOverrides,
} from '@/lib/color-themes';
import { STORAGE_KEYS } from '@/lib/storage-keys';
import { ThemeProviderContext } from '@/hooks/useTheme';

type Theme = 'dark' | 'light' | 'system';

interface ThemeProviderProps {
  children: React.ReactNode;
  defaultTheme?: Theme;
  storageKey?: string;
}

export function ThemeProvider({
  children,
  defaultTheme = 'system',
  storageKey = STORAGE_KEYS.theme,
  ...props
}: ThemeProviderProps) {
  const [theme, setTheme] = useState<Theme>(
    () => (localStorage.getItem(storageKey) as Theme) || defaultTheme,
  );

  const [colorTheme, setColorThemeState] = useState<ColorThemeId | null>(
    () =>
      (localStorage.getItem(STORAGE_KEYS.colorTheme) as ColorThemeId) ||
      null,
  );

  // Apply dark/light class
  useEffect(() => {
    const root = window.document.documentElement;

    root.classList.remove('light', 'dark');

    if (theme === 'system') {
      const systemTheme = window.matchMedia('(prefers-color-scheme: dark)')
        .matches
        ? 'dark'
        : 'light';
      root.classList.add(systemTheme);
    } else {
      root.classList.add(theme);
    }
  }, [theme]);

  // Apply color theme CSS vars whenever mode or color theme changes
  useEffect(() => {
    if (colorTheme && colorTheme in colorThemes) {
      applyColorTheme(colorTheme);
    } else {
      clearColorThemeOverrides();
    }
  }, [theme, colorTheme]);

  const setColorTheme = (id: ColorThemeId | null) => {
    if (id) {
      localStorage.setItem(STORAGE_KEYS.colorTheme, id);
    } else {
      localStorage.removeItem(STORAGE_KEYS.colorTheme);
    }
    setColorThemeState(id);
  };

  const value = {
    theme,
    setTheme: (theme: Theme) => {
      localStorage.setItem(storageKey, theme);
      setTheme(theme);
    },
    colorTheme,
    setColorTheme,
  };

  return (
    <ThemeProviderContext.Provider {...props} value={value}>
      {children}
    </ThemeProviderContext.Provider>
  );
}
