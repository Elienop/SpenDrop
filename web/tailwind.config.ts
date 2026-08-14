import type { Config } from 'tailwindcss';
import plugin from 'tailwindcss/plugin';

const config: Config = {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  corePlugins: {
    preflight: true,
  },
  theme: {
    container: {
      center: true,
      padding: '2rem',
      screens: { '2xl': '1400px' },
    },
    extend: {
      fontFamily: {
        sans: ['"Geist Variable"', 'system-ui', 'sans-serif'],
        mono: ['"Geist Mono Variable"', 'ui-monospace', 'monospace'],
      },
      colors: {
        border: 'hsl(var(--border))',
        input: 'hsl(var(--input))',
        ring: 'hsl(var(--ring))',
        background: 'hsl(var(--background))',
        foreground: 'hsl(var(--foreground))',
        primary: {
          DEFAULT: 'hsl(var(--primary))',
          foreground: 'hsl(var(--primary-foreground))',
        },
        secondary: {
          DEFAULT: 'hsl(var(--secondary))',
          foreground: 'hsl(var(--secondary-foreground))',
        },
        destructive: {
          DEFAULT: 'hsl(var(--destructive))',
          foreground: 'hsl(var(--destructive-foreground))',
        },
        muted: {
          DEFAULT: 'hsl(var(--muted))',
          foreground: 'hsl(var(--muted-foreground))',
        },
        accent: {
          DEFAULT: 'hsl(var(--accent))',
          foreground: 'hsl(var(--accent-foreground))',
        },
        popover: {
          DEFAULT: 'hsl(var(--popover))',
          foreground: 'hsl(var(--popover-foreground))',
        },
        card: {
          DEFAULT: 'hsl(var(--card))',
          foreground: 'hsl(var(--card-foreground))',
        },
        // Chart palette mirrors the --chart-N CSS variables in
        // globals.css. Keep this block in sync with globals.css: a
        // missing slot here means the class `bg-chart-N` silently
        // becomes a no-op even though the CSS variable exists.
        chart: {
          '1': 'hsl(var(--chart-1))',
          '2': 'hsl(var(--chart-2))',
          '3': 'hsl(var(--chart-3))',
          '4': 'hsl(var(--chart-4))',
          '5': 'hsl(var(--chart-5))',
          '6': 'hsl(var(--chart-6))',
          '7': 'hsl(var(--chart-7))',
          '8': 'hsl(var(--chart-8))',
          '9': 'hsl(var(--chart-9))',
          '10': 'hsl(var(--chart-10))',
          '11': 'hsl(var(--chart-11))',
          '12': 'hsl(var(--chart-12))',
          '13': 'hsl(var(--chart-13))',
          '14': 'hsl(var(--chart-14))',
          '15': 'hsl(var(--chart-15))',
          '16': 'hsl(var(--chart-16))',
          '17': 'hsl(var(--chart-17))',
          '18': 'hsl(var(--chart-18))',
          '19': 'hsl(var(--chart-19))',
          '20': 'hsl(var(--chart-20))',
        },
      },
      borderRadius: {
        lg: 'var(--radius)',
        md: 'calc(var(--radius) - 2px)',
        sm: 'calc(var(--radius) - 4px)',
      },
      keyframes: {
        'accordion-down': {
          from: { height: '0' },
          to: { height: 'var(--radix-accordion-content-height)' },
        },
        'accordion-up': {
          from: { height: 'var(--radix-accordion-content-height)' },
          to: { height: '0' },
        },
      },
      animation: {
        'accordion-down': 'accordion-down 0.2s ease-out',
        'accordion-up': 'accordion-up 0.2s ease-out',
      },
    },
  },
  plugins: [
    require('tailwindcss-animate'),
    // `coarse:` — a POINTER gate, not a width gate. The two are not
    // interchangeable and the difference is measured, not theoretical: the
    // household's Galaxy Tab S10 FE is ~1130px in landscape, so it takes the
    // desktop side of every `md:` breakpoint while still being a touch screen.
    // A control sized `h-11 md:h-8` gives that tablet a 32px target. Gating on
    // `(pointer: coarse)` instead asks the question the 44px floor is actually
    // about — "is this a finger?" — and leaves a mouse desktop at any width
    // untouched, which is the owner's explicit constraint.
    //
    // Tailwind v4 ships this as the built-in `pointer-coarse:`; until the
    // upgrade this plugin is the local equivalent. See `src/lib/touch-target.ts`
    // for when to reach for it versus the hit-area-only recipe.
    plugin(({ addVariant }) => {
      addVariant('coarse', '@media (pointer: coarse)');
    }),
  ],
};

export default config;
