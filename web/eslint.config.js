import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist', 'coverage', 'playwright-report', 'test-results', 'src/api/schema.d.ts'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: { ecmaVersion: 2022, globals: { ...globals.browser, ...globals.node } },
    plugins: { 'react-hooks': reactHooks, 'react-refresh': reactRefresh },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      '@typescript-eslint/consistent-type-imports': 'error',
    },
  },
  {
    // UI kit, widget definitions (export a definition object next to the
    // component) and context modules (provider + hook) are fine without fast refresh.
    files: [
      'src/components/ui/**',
      'src/widgets/**',
      'src/**/*.test.{ts,tsx}',
      'e2e/**',
      'src/lib/theme.tsx',
      'src/realtime/context.tsx',
      'src/components/ConfirmDialog.tsx',
      'src/components/JsonEditor.tsx',
      'src/features/dashboards/DashboardGrid.tsx',
      'src/features/dashboards/WidgetFrame.tsx',
    ],
    rules: { 'react-refresh/only-export-components': 'off' },
  },
)
