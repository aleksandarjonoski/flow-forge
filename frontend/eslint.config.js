// ESLint flat config: TypeScript rules with type information, plus Lit and
// web-component rules. eslint-config-prettier goes last so formatting is left
// entirely to Prettier.
import js from '@eslint/js'
import prettier from 'eslint-config-prettier'
import lit from 'eslint-plugin-lit'
import wc from 'eslint-plugin-wc'
import globals from 'globals'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist/'] },
  js.configs.recommended,
  {
    files: ['src/**/*.ts'],
    extends: [
      tseslint.configs.recommendedTypeChecked,
      wc.configs['flat/recommended'],
      lit.configs['flat/recommended'],
    ],
    languageOptions: {
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ['*.js'],
    languageOptions: { globals: globals.node },
  },
  prettier,
)
