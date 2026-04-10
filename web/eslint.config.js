import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
  },
  // shadcn/ui primitives export variants + types alongside components
  {
    files: ['src/components/ui/**/*.{ts,tsx}', 'src/hooks/useAuth.tsx'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
  // Data-fetching hooks use setState inside useEffect — established pattern
  {
    files: ['src/hooks/**/*.{ts,tsx}', 'src/pages/**/*.{ts,tsx}'],
    rules: {
      'react-hooks/set-state-in-effect': 'off',
    },
  },
  // Tailwind config uses require() for plugins
  {
    files: ['tailwind.config.ts'],
    rules: {
      '@typescript-eslint/no-require-imports': 'off',
    },
  },
])
