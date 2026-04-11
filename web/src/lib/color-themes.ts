/**
 * shadcn accent color themes — HSL values for Tailwind v3.
 *
 * Each theme overrides only the primary accent CSS variables.
 * The base neutral tokens (background, card, muted, border, etc.)
 * and chart category colors stay as defined in globals.css.
 *
 * "Neutral" is the default — selecting it clears all overrides.
 * Values converted from shadcn v4 OKLCH source via culori (gamut-clamped).
 */

export type ColorThemeId =
  | 'neutral'
  | 'blue'
  | 'green'
  | 'orange'
  | 'red'
  | 'rose'
  | 'violet'
  | 'yellow';

interface ColorTheme {
  label: string;
  swatch: string; // representative hex for the preview dot
  light: Record<string, string>;
  dark: Record<string, string>;
}

export const colorThemes: Record<ColorThemeId, ColorTheme> = {
  neutral: {
    label: 'Neutral',
    swatch: '#737373',
    light: {},
    dark: {},
  },

  blue: {
    label: 'Blue',
    swatch: '#1447e6',
    light: {
      '--primary': '225.3 84.1% 49%',
      '--primary-foreground': '213.8 96.5% 96.8%',
    },
    dark: {
      '--primary': '227.1 75.7% 41.1%',
      '--primary-foreground': '213.8 96.5% 96.8%',
    },
  },

  green: {
    label: 'Green',
    swatch: '#008236',
    light: {
      '--primary': '144.7 100% 25.5%',
      '--primary-foreground': '138.5 76.5% 96.7%',
    },
    dark: {
      '--primary': '147.7 97.2% 20.4%',
      '--primary-foreground': '138.5 76.5% 96.7%',
    },
  },

  orange: {
    label: 'Orange',
    swatch: '#ca3500',
    light: {
      '--primary': '15.7 100% 39.6%',
      '--primary-foreground': '33.8 100% 96.5%',
    },
    dark: {
      '--primary': '17 100% 31.2%',
      '--primary-foreground': '33.8 100% 96.5%',
    },
  },

  red: {
    label: 'Red',
    swatch: '#c10007',
    light: {
      '--primary': '357.7 100% 37.8%',
      '--primary-foreground': '360 88.2% 97.3%',
    },
    dark: {
      '--primary': '355.9 91.2% 32.5%',
      '--primary-foreground': '360 88.2% 97.3%',
    },
  },

  rose: {
    label: 'Rose',
    swatch: '#c70036',
    light: {
      '--primary': '343.8 100% 38.9%',
      '--primary-foreground': '355.7 96.5% 97.2%',
    },
    dark: {
      '--primary': '340.4 100% 32.3%',
      '--primary-foreground': '355.7 96.5% 97.2%',
    },
  },

  violet: {
    label: 'Violet',
    swatch: '#7008e7',
    light: {
      '--primary': '268 93.3% 46.9%',
      '--primary-foreground': '250 98.6% 97.6%',
    },
    dark: {
      '--primary': '266.6 86.3% 40.4%',
      '--primary-foreground': '250 98.6% 97.6%',
    },
  },

  yellow: {
    label: 'Yellow',
    swatch: '#fdc700',
    light: {
      '--primary': '47.2 100% 49.7%',
      '--primary-foreground': '29.5 83.4% 24.6%',
    },
    dark: {
      '--primary': '44.2 100% 47.1%',
      '--primary-foreground': '29.5 83.4% 24.6%',
    },
  },
};

export const colorThemeIds = Object.keys(colorThemes) as ColorThemeId[];

/** CSS var keys that accent themes override. */
const ACCENT_KEYS = ['--primary', '--primary-foreground'];

/** Legacy keys from previous implementations that may linger as inline styles. */
const LEGACY_KEYS = [
  '--background', '--foreground', '--card', '--card-foreground',
  '--popover', '--popover-foreground', '--secondary', '--secondary-foreground',
  '--muted', '--muted-foreground', '--accent', '--accent-foreground',
  '--destructive', '--destructive-foreground', '--border', '--input', '--ring',
  '--chart-1', '--chart-2', '--chart-3', '--chart-4', '--chart-5',
  '--chart-6', '--chart-7', '--chart-8', '--chart-9', '--chart-10',
  '--chart-11', '--chart-12', '--chart-13', '--chart-14', '--chart-15',
  '--chart-16', '--chart-17', '--chart-18', '--chart-19', '--chart-20',
];

/** Apply an accent theme's CSS variables to the document root. */
export function applyColorTheme(id: ColorThemeId): void {
  const root = document.documentElement;

  // Always clear stale legacy overrides first
  for (const key of LEGACY_KEYS) {
    root.style.removeProperty(key);
  }

  // Neutral = clear all overrides → fall back to globals.css
  if (id === 'neutral') {
    for (const key of ACCENT_KEYS) {
      root.style.removeProperty(key);
    }
    return;
  }

  const isDark = root.classList.contains('dark');
  const vars = isDark ? colorThemes[id].dark : colorThemes[id].light;

  for (const key of ACCENT_KEYS) {
    if (vars[key]) {
      root.style.setProperty(key, vars[key]);
    }
  }
}

/** Remove all inline accent-theme overrides (falls back to globals.css). */
export function clearColorThemeOverrides(): void {
  const root = document.documentElement;
  for (const key of [...ACCENT_KEYS, ...LEGACY_KEYS]) {
    root.style.removeProperty(key);
  }
}
